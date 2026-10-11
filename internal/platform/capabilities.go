package platform

func Supports(available, required []string) bool {
	if len(available) > 8 || len(required) > 8 {
		return false
	}
	set := map[string]bool{}
	for _, v := range available {
		set[v] = true
	}
	for _, v := range required {
		if !set[v] {
			return false
		}
	}
	return true
}
