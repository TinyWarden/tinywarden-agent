package maintenance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
)

func isMissing(e error) bool { return os.IsNotExist(e) }
func chunkLength(raw string) (int, error) {
	b, e := base64.StdEncoding.Strict().DecodeString(raw)
	return len(b), e
}
func terminal(phase string) bool {
	return phase == "succeeded" || phase == "no_changes" || phase == "blocked" || phase == "failed" || phase == "expired" || phase == "cancelled"
}

type ResultError interface {
	error
	MaintenanceResult() Result
}

func failure(e error, effect string) Result {
	if typed, ok := e.(ResultError); ok {
		return typed.MaintenanceResult()
	}
	reason := "maintenance_worker_interrupted"
	return Result{Effect: effect, Reason: &reason, Facts: json.RawMessage(`{}`)}
}
func (c Coordinator) failed(j *Journal, result Result, prepareEffect string) error {
	// A later phase's no-change refusal cannot erase completed preparation.
	// Use the definition already bound by Work, even if a later probe is refused.
	// Keep unknown effects unknown, and pre-preparation refusals unchanged.
	if j.PreparedAt != nil && prepareEffect == "changed" && result.Effect == "none" {
		result.Effect = "changed"
	}
	if len(result.Facts) == 0 || string(result.Facts) == "{}" {
		if d, e := c.Registry.ResolveIntent(j.Intent); e == nil && len(d.ErrorFacts) > 0 {
			result.Facts = d.ErrorFacts
		}
	}
	j.Result = &result
	j.Phase = "terminal"
	j.Unsent = "failed"
	j.Attention = j.ApplyAttempt != nil || result.ProcessRunning
	if e := Save(c.Base, *j); e != nil {
		return e
	}
	return c.queue(j, "failed", map[string]any{"result": result})
}
func (c Coordinator) serverState(j *Journal, d Delivery, prepareEffect string) (bool, error) {
	if e := c.bind(j, d); e != nil {
		return false, e
	}
	if terminal(d.Phase) {
		if d.Result == nil {
			return false, errors.New("maintenance_reply_invalid")
		}
		if j.ApplyAttempt != nil && j.Result == nil {
			return false, errors.New("maintenance_reconciliation_required")
		}
		if (d.Phase == "succeeded" || d.Phase == "no_changes") && j.ApplyAttempt != nil && !j.Result.LocalVerified {
			return false, errors.New("maintenance_reconciliation_required")
		}
		j.Result = d.Result
		j.Phase = "terminal"
		j.Attention = d.Attention
		if e := Save(c.Base, *j); e != nil {
			return false, e
		}
		if !j.Attention && !d.Result.ProcessRunning {
			return true, Forget(c.Base, j)
		}
		return true, nil
	}
	if d.CancelRequested && j.ApplyAttempt == nil && len(j.Pending) == 0 && j.Unsent == "" {
		reason := "maintenance_cancelled"
		effect := "none"
		if j.PreparedAt != nil || j.Phase == "preparing" {
			effect = prepareEffect
		}
		j.Phase = "terminal"
		j.Unsent = "cancelled"
		j.Result = &Result{Effect: effect, Reason: &reason, Facts: json.RawMessage(`{}`)}
		if e := Save(c.Base, *j); e != nil {
			return false, e
		}
		return false, c.queue(j, "cancelled", struct{}{})
	}
	return false, nil
}

// Work holds no process across human approval. The installation owner holds the
// common root mutation lock for this invocation and passes it to native children.
func (c Coordinator) Work(ctx context.Context, j *Journal) error {
	if j.Intent.Scope != c.Scope || j.Acknowledged {
		return errors.New("maintenance_scope_changed")
	}
	definition, e := c.Registry.ResolveIntent(j.Intent)
	if e != nil {
		return e
	}
	provider := definition.New()
	prepareEffect := definition.PrepareEffect
	if prepareEffect == "" {
		prepareEffect = "indexes_changed"
	} // Legacy journal fixtures only.
	// Resuming an applying journal is receipt recovery ONLY. Never call Apply again.
	if j.Phase == "applying" {
		if j.PlanSHA == nil || j.Start == nil {
			return errors.New("maintenance_journal_invalid")
		}
		result, e := provider.Recover(*j.PlanSHA, *j.Start)
		if e != nil {
			if e = c.failed(j, failure(e, "unknown"), prepareEffect); e != nil {
				return e
			}
		} else {
			if !result.LocalVerified || result.ProcessRunning || result.Reason != nil {
				return errors.New("maintenance_result_invalid")
			}
			j.Result = &result
			j.Phase = "verifying"
			j.Unsent = "verifying"
			if e = Save(c.Base, *j); e != nil {
				return e
			}
			if e = c.queue(j, "verifying", map[string]any{"result": result}); e != nil {
				return e
			}
		}
	} else if j.Phase == "preparing" || j.Phase == "downloading" && j.FetchStarted && !j.Downloaded {
		if e = c.failed(j, failure(errors.New("interrupted"), prepareEffect), prepareEffect); e != nil {
			return e
		}
	}
	for steps := 0; steps < MaxEvents+8; steps++ {
		if len(j.Pending) == 0 && j.Unsent != "" {
			data := any(struct{}{})
			if j.Unsent == "failed" || j.Unsent == "verifying" || j.Unsent == "completed" {
				data = map[string]any{"result": j.Result}
			}
			if e = c.queue(j, j.Unsent, data); e != nil {
				return e
			}
		}
		var reply Reply
		if len(j.Pending) > 0 {
			reply, e = c.exchange(ctx, j)
		} else {
			reply, e = c.poll(ctx, j)
		}
		if e != nil {
			return e
		}
		if reply.Job == nil {
			return nil
		}
		stop, e := c.serverState(j, *reply.Job, prepareEffect)
		if e != nil || stop {
			return e
		}
		if len(j.Pending) > 0 {
			continue
		}
		switch j.Phase {
		case "queued":
			if reply.Job.Phase != "preparing" {
				return errors.New("maintenance_transition_invalid")
			}
			j.Phase = "preparing"
			if e = Save(c.Base, *j); e != nil {
				return e
			}
			prepared, e := safePhase(ctx, provider.Prepare)
			if e != nil {
				if e = c.failed(j, failure(e, prepareEffect), prepareEffect); e != nil {
					return e
				}
				continue
			}
			j.PreparedAt, e = instant()
			if e != nil {
				return e
			}
			if !definition.Approval {
				if prepared.Result == nil || !prepared.Result.LocalVerified || prepared.Result.ProcessRunning {
					return errors.New("maintenance_result_invalid")
				}
				j.Result = prepared.Result
				j.Phase = "terminal"
				j.Unsent = "completed"
				if e = Save(c.Base, *j); e != nil {
					return e
				}
				if e = c.queue(j, "completed", map[string]any{"result": j.Result}); e != nil {
					return e
				}
				continue
			}
			if e = SavePlan(c.Base, j, prepared.Plan, prepared.Digest); e != nil {
				return e
			}
		case "awaiting_approval":
			if e = planFresh(j); e != nil {
				if e = c.failed(j, failure(e, prepareEffect), prepareEffect); e != nil {
					return e
				}
				continue
			}
			plan, e := ReadPlan(c.Base)
			if e != nil {
				return e
			}
			if !j.PrepareNotified {
				at, e := instant()
				if e != nil {
					return e
				}
				if e = c.queue(j, "prepared", map[string]any{"provider_sha256": j.PlanSHA, "parts": (len(plan) + ChunkBytes - 1) / ChunkBytes, "age_ms": at.Milliseconds - j.PreparedAt.Milliseconds}); e != nil {
					return e
				}
				continue
			}
			if j.UploadedBytes < len(plan) {
				end := j.UploadedBytes + ChunkBytes
				if end > len(plan) {
					end = len(plan)
				}
				if e = c.queue(j, "plan_chunk", map[string]any{"provider_sha256": j.PlanSHA, "part": j.UploadedBytes / ChunkBytes, "total": (len(plan) + ChunkBytes - 1) / ChunkBytes, "content": base64.StdEncoding.EncodeToString(plan[j.UploadedBytes:end])}); e != nil {
					return e
				}
				continue
			}
			if reply.Job.Phase != "approved" {
				return nil
			}
			j.Phase = "approved"
			if e = Save(c.Base, *j); e != nil {
				return e
			}
			if e = c.queue(j, "downloading", struct{}{}); e != nil {
				return e
			}
		case "approved":
			if e = c.queue(j, "downloading", struct{}{}); e != nil {
				return e
			}
		case "downloading":
			if e = planFresh(j); e != nil {
				if e = c.failed(j, failure(e, prepareEffect), prepareEffect); e != nil {
					return e
				}
				continue
			}
			if !j.Downloaded {
				j.FetchStarted = true
				if e = Save(c.Base, *j); e != nil {
					return e
				}
				if _, e = safePhase(ctx, func(ctx context.Context) (struct{}, error) { return struct{}{}, provider.Fetch(ctx, *j.PlanSHA) }); e != nil {
					if e = c.failed(j, failure(e, prepareEffect), prepareEffect); e != nil {
						return e
					}
					continue
				}
				j.Downloaded = true
				if e = Save(c.Base, *j); e != nil {
					return e
				}
			}
			if e = c.queue(j, "ready", struct{}{}); e != nil {
				return e
			}
		case "applying":
			// Only reachable after the atomic StartApply in THIS invocation's exchange.
			result, e := provider.Apply(*j.PlanSHA, *j.Start)
			if e != nil {
				if e = c.failed(j, failure(e, "unknown"), prepareEffect); e != nil {
					return e
				}
				continue
			}
			if !result.LocalVerified || result.ProcessRunning || result.Reason != nil {
				return errors.New("maintenance_result_invalid")
			}
			j.Result = &result
			j.Phase = "verifying"
			j.Unsent = "verifying"
			if e = Save(c.Base, *j); e != nil {
				return e
			}
			if e = c.queue(j, "verifying", map[string]any{"result": result}); e != nil {
				return e
			}
		case "verifying", "terminal":
			return nil
		default:
			return errors.New("maintenance_transition_invalid")
		}
	}
	return errors.New("maintenance_sequence_invalid")
}
