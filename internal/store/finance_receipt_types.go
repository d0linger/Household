package store

import "strconv"

// FinanceParseReceiptAmount uses the explicitly selected decimal/group syntax.
func FinanceParseReceiptAmount(raw string, exponent int, decimal, group string) (string, error) {
	n, err := financeParseBankDecimal(raw, exponent, decimal, group)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(n, 10), nil
}

type FinanceReceiptSource struct {
	AttachmentID  string
	StorageKey    string
	MediaType     string
	Name          string
	SizeBytes     int64
	SHA256        string
	SourceVersion int64
	AccessVersion string
	Generation    int64
	CanReview     bool
}

type FinanceReceiptRecognizeInput struct {
	Generation       int64  `json:"generation"`
	SourceVersion    int64  `json:"source_version"`
	SourceHash       string `json:"source_hash"`
	Currency         string `json:"currency"`
	Exponent         int    `json:"exponent"`
	Pages            []int  `json:"pages"`
	DecimalSeparator string `json:"decimal_separator"`
	GroupSeparator   string `json:"group_separator"`
}

type FinanceReceiptTotalCandidate struct {
	Minor      string  `json:"minor"`
	PageNumber int     `json:"page_number"`
	SourceLine int     `json:"source_line"`
	SourceText string  `json:"source_text"`
	Confidence *string `json:"confidence"`
}

type FinanceReceiptSuggestions struct {
	RecognizedPages     []int                          `json:"recognized_pages"`
	PageCount           *int                           `json:"page_count"`
	Lines               []FinanceReceiptLine           `json:"lines"`
	TotalCandidates     []FinanceReceiptTotalCandidate `json:"total_candidates"`
	AmbiguousTotal      bool                           `json:"ambiguous_total"`
	RecognitionComplete bool                           `json:"recognition_complete"`
}
