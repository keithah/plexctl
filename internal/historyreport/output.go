package historyreport

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	csvExtension   = ".csv"
	jsonlExtension = ".jsonl"
)

// Export appends normalized public view records to a CSV or JSONL file selected
// by its lowercase extension. It validates and renders the complete batch before
// opening the destination, and it never creates a file for an empty batch.
func Export(path string, views []View) error {
	format, err := exportFormat(path)
	if err != nil {
		return err
	}
	if len(views) == 0 {
		return nil
	}

	existingSize, err := outputSize(path)
	if err != nil {
		return err
	}

	var batch []byte
	switch format {
	case csvExtension:
		batch, err = renderCSV(views, existingSize > 0)
	case jsonlExtension:
		batch, err = renderJSONL(views)
	}
	if err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open export output: %w", err)
	}
	if _, err := file.Write(batch); err != nil {
		_ = file.Close()
		return fmt.Errorf("append export output: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close export output: %w", err)
	}
	return nil
}

func exportFormat(path string) (string, error) {
	switch {
	case strings.HasSuffix(path, csvExtension):
		return csvExtension, nil
	case strings.HasSuffix(path, jsonlExtension):
		return jsonlExtension, nil
	default:
		return "", fmt.Errorf("unsupported export output extension for %q: use .csv or .jsonl", path)
	}
}

func outputSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err == nil {
		return info.Size(), nil
	}
	if os.IsNotExist(err) {
		return 0, nil
	}
	return 0, fmt.Errorf("stat export output: %w", err)
}

func renderCSV(views []View, hasContent bool) ([]byte, error) {
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	if !hasContent {
		if err := writer.Write(exportCSVHeader); err != nil {
			return nil, fmt.Errorf("render CSV header: %w", err)
		}
	}
	for _, view := range views {
		if err := writer.Write(exportRecordFromView(view).csvRow()); err != nil {
			return nil, fmt.Errorf("render CSV row: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, fmt.Errorf("render CSV: %w", err)
	}
	return output.Bytes(), nil
}

func renderJSONL(views []View) ([]byte, error) {
	var output bytes.Buffer
	for _, view := range views {
		encoded, err := json.Marshal(exportRecordFromView(view))
		if err != nil {
			return nil, fmt.Errorf("render JSONL row: %w", err)
		}
		output.Write(encoded)
		output.WriteByte('\n')
	}
	return output.Bytes(), nil
}

type exportRecord struct {
	RatingKey        string  `json:"rating_key"`
	Title            string  `json:"title"`
	ParentTitle      string  `json:"parent_title,omitempty"`
	GrandparentTitle string  `json:"grandparent_title,omitempty"`
	MediaType        string  `json:"media_type,omitempty"`
	SectionID        string  `json:"section_id,omitempty"`
	SectionTitle     string  `json:"section_title,omitempty"`
	AccountID        string  `json:"account_id,omitempty"`
	AccountTitle     string  `json:"account_title,omitempty"`
	ViewedAt         string  `json:"viewed_at,omitempty"`
	Duration         *string `json:"duration,omitempty"`
}

func exportRecordFromView(view View) exportRecord {
	record := exportRecord{
		RatingKey:        view.RatingKey,
		Title:            view.Title,
		ParentTitle:      view.ParentTitle,
		GrandparentTitle: view.GrandparentTitle,
		MediaType:        view.MediaType,
		SectionID:        view.SectionID,
		SectionTitle:     view.SectionTitle,
		AccountID:        view.AccountID,
		AccountTitle:     view.AccountTitle,
	}
	if !view.ViewedAt.IsZero() {
		record.ViewedAt = view.ViewedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	if view.Duration != nil {
		duration := view.Duration.String()
		record.Duration = &duration
	}
	return record
}

func (record exportRecord) csvRow() []string {
	duration := ""
	if record.Duration != nil {
		duration = *record.Duration
	}
	return []string{
		record.RatingKey,
		record.Title,
		record.ParentTitle,
		record.GrandparentTitle,
		record.MediaType,
		record.SectionID,
		record.SectionTitle,
		record.AccountID,
		record.AccountTitle,
		record.ViewedAt,
		duration,
	}
}
