package store

// ProfileName projects the editable core account name into the console's
// ten-character display field. It never changes the NNID or binary Mii data.
func ProfileName(name string) string {
	chars := []rune(name)
	if len(chars) > 10 {
		chars = chars[:10]
	}
	return string(chars)
}
