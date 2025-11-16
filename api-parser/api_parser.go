package apiparser

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

type APIField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

type APIDoc struct {
	Name        string     `json:"name"`
	Path        string     `json:"path"`
	Method      string     `json:"method"`
	Description string     `json:"description"`
	Fields      []APIField `json:"fields"`
}

func ParseAPIDocs(path string) ([]APIDoc, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var apis []APIDoc
	var current APIDoc
	var inFields bool

	scanner := bufio.NewScanner(file)
	reHeader := regexp.MustCompile(`^###\s*(.+)`)
	rePath := regexp.MustCompile(`\*\*Path:\*\*\s*(.+)`)
	reMethod := regexp.MustCompile(`\*\*Method:\*\*\s*(.+)`)
	reDesc := regexp.MustCompile(`\*\*Description:\*\*\s*(.+)`)
	reField := regexp.MustCompile(`-\s*name:\s*([^\s]+)\s*type:\s*([^\s]+)\s*description:\s*(.+)`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines or separators
		if line == "" || strings.HasPrefix(line, "---") {
			continue
		}

		// New API section
		if matches := reHeader.FindStringSubmatch(line); matches != nil {
			// Save previous API if it exists
			if current.Name != "" {
				apis = append(apis, current)
			}
			current = APIDoc{Name: matches[1]}
			inFields = false
			continue
		}

		if matches := rePath.FindStringSubmatch(line); matches != nil {
			current.Path = strings.TrimSpace(matches[1])
			continue
		}

		if matches := reMethod.FindStringSubmatch(line); matches != nil {
			current.Method = strings.TrimSpace(matches[1])
			continue
		}

		if matches := reDesc.FindStringSubmatch(line); matches != nil {
			current.Description = strings.TrimSpace(matches[1])
			continue
		}

		if strings.HasPrefix(line, "**Fields:**") {
			inFields = true
			continue
		}

		if inFields && strings.HasPrefix(line, "-") {
			// Try to parse full inline field definition (one-liner)
			if matches := reField.FindStringSubmatch(line); matches != nil {
				field := APIField{
					Name:        matches[1],
					Type:        matches[2],
					Description: matches[3],
				}
				current.Fields = append(current.Fields, field)
				continue
			}

			// Handle multiline field entries:
			field := parseField(line)
			if field != nil {
				current.Fields = append(current.Fields, *field)
			}
		}
	}

	// Add last API
	if current.Name != "" {
		apis = append(apis, current)
	}

	return apis, scanner.Err()
}

func parseField(line string) *APIField {
	line = strings.TrimPrefix(line, "-")
	parts := strings.Split(line, "  ")
	field := APIField{}

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "name:") {
			field.Name = strings.TrimSpace(strings.TrimPrefix(part, "name:"))
		} else if strings.HasPrefix(part, "type:") {
			field.Type = strings.TrimSpace(strings.TrimPrefix(part, "type:"))
		} else if strings.HasPrefix(part, "description:") {
			field.Description = strings.TrimSpace(strings.TrimPrefix(part, "description:"))
		}
	}
	if field.Name == "" {
		return nil
	}
	return &field
}
