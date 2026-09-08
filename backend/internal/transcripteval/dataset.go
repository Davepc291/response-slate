package transcripteval

import (
	"bufio"
	"bytes"
)

// Record is the unchanged Step 4A input contract. Experiments must not add fields.
type Record = record

// DecodeDataset exposes the existing strict validation without relaxing Step 4A.
// It reads no files, contacts no services, and returns records only after the
// whole bounded dataset passes evaluation validation.
func DecodeDataset(data []byte) ([]Record, Report, error) {
	if len(data) > MaxFileBytes {
		return nil, Report{}, ErrInput
	}
	report, err := evaluate(bytes.NewReader(data))
	if err != nil {
		return nil, Report{}, err
	}
	scan := bufio.NewScanner(bytes.NewReader(data))
	scan.Buffer(make([]byte, 4096), MaxLineBytes+2)
	var records []Record
	for scan.Scan() {
		r, err := parse(scan.Bytes())
		if err != nil {
			return nil, Report{}, err
		}
		records = append(records, r)
	}
	return records, report, scan.Err()
}

// DecodeRecords validates the same contract without calculating accuracy metrics.
// Experiment callers filter the explicit split before resolving any evidence.
func DecodeRecords(data []byte) ([]Record, error) {
	if len(data) > MaxFileBytes {
		return nil, ErrInput
	}
	scan := bufio.NewScanner(bytes.NewReader(data))
	scan.Buffer(make([]byte, 4096), MaxLineBytes+2)
	var records []Record
	ids, fps := map[string]bool{}, map[string]bool{}
	cells := 0
	for scan.Scan() {
		if len(scan.Bytes()) > MaxLineBytes || len(records) >= MaxRecords {
			return nil, ErrInput
		}
		r, e := parse(scan.Bytes())
		if e != nil || ids[r.DatasetID] || fps[r.Fingerprint] {
			return nil, ErrInput
		}
		ids[r.DatasetID] = true
		fps[r.Fingerprint] = true
		a, b := len(tokens(r.Reference)), len(tokens(r.Raw))
		cells += a * b
		if a > MaxTokens || b > MaxTokens || cells > MaxCells {
			return nil, ErrInput
		}
		records = append(records, r)
	}
	if scan.Err() != nil || len(records) == 0 {
		return nil, ErrInput
	}
	return records, nil
}
