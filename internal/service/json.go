package service

import "strings"

// NormalizeJSON removes optional Markdown fences and surrounding model text.
// JSON decoding remains the authority for validating the extracted payload.
func NormalizeJSON(response string) string {
	response = strings.TrimSpace(response)
	response = strings.TrimPrefix(response, "﻿")
	response = strings.ReplaceAll(response, "```json", "")
	response = strings.ReplaceAll(response, "```JSON", "")
	response = strings.ReplaceAll(response, "```", "")
	response = strings.TrimSpace(response)

	startObject := strings.Index(response, "{")
	startArray := strings.Index(response, "[")
	start := -1
	switch {
	case startObject < 0:
		start = startArray
	case startArray < 0:
		start = startObject
	case startObject < startArray:
		start = startObject
	default:
		start = startArray
	}
	if start < 0 {
		return response
	}

	opening := response[start]
	closing := byte('}')
	if opening == '[' {
		closing = ']'
	}
	end := strings.LastIndexByte(response, closing)
	if end < start {
		return response[start:]
	}
	return strings.TrimSpace(response[start : end+1])
}
