package dto

type ReportRequest struct {
	ReasonCode  string `json:"reason_code"`
	Description string `json:"description"` // optional
}

type ReportResults struct {
	Reported bool `json:"reported"`
}
