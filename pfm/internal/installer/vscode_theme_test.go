package installer

// merged copies base and sets over's entries on it.
func merged(base map[string]any, over ...map[string]string) map[string]any {
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	for _, layer := range over {
		for key, value := range layer {
			out[key] = value
		}
	}
	return out
}
