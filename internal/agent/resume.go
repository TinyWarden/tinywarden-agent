package agent

import "context"

// ResumeEnrollment repeats only the existing persisted handshake. It never creates credentials.
func ResumeEnrollment(ctx context.Context, config Config, client *Client) error {
	store, err := OpenStore(config.StateDir)
	if err != nil {
		return err
	}
	defer store.Close()
	state, err := store.Load()
	if err != nil {
		return err
	}
	if state.Enrollment == nil {
		return nil
	}
	return finishEnrollment(ctx, store, &state, client)
}
