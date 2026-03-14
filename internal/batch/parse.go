package batch

func getStr(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// MediaItemFromMap parses a map into MediaItem.
func MediaItemFromMap(m map[string]interface{}) MediaItem {
	item := MediaItem{
		S3Key:     getStr(m, "s3_key"),
		MediaType: getStr(m, "media_type"),
		Filename:  getStr(m, "filename"),
		DateTaken: getStr(m, "date_taken"),
	}
	if g, ok := m["gps"].(map[string]interface{}); ok {
		lat, _ := g["latitude"].(float64)
		lon, _ := g["longitude"].(float64)
		item.GPS = &GPS{Latitude: lat, Longitude: lon}
	}
	return item
}
