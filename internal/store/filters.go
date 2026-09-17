package store

import (
	"slices"
	"strings"

	"lt-api.aleksrdvn.com/internal/validator"
)

// Filters carries the standard list-query pagination parameters shared by
// every list endpoint and every List store method: page/page_size bounds the
// result window, sort names the column (safelist-validated, "-prefix means
// descending), and SortSafelist is the per-resource whitelist that makes
// SortColumn safe to interpolate into SQL.
type Filters struct {
	Page         int
	PageSize     int
	Sort         string
	SortSafelist []string
}

// SortColumn returns the SQL column name for the sort parameter. It panics
// on an unsafelisted value: only ValidateFilters-checked filters may reach
// it, and the panic is the guard that keeps fmt.Sprintf-built queries safe.
func (f Filters) SortColumn() string {
	if slices.Contains(f.SortSafelist, f.Sort) {
		return strings.TrimPrefix(f.Sort, "-")
	}

	panic("unsafe sort parameter: " + f.Sort)
}

func (f Filters) SortDirection() string {
	if strings.HasPrefix(f.Sort, "-") {
		return "DESC"
	}
	return "ASC"
}

func (f Filters) Limit() int {
	return f.PageSize
}

func (f Filters) Offset() int {
	return (f.Page - 1) * f.PageSize
}

func ValidateFilters(v *validator.Validator, f Filters) {
	v.Check(f.Page > 0, "page", "must be greater than zero")
	v.Check(f.Page <= 10_000_000, "page", "must be a maximum of 10 million")
	v.Check(f.PageSize > 0, "page_size", "must be greater than zero")
	v.Check(f.PageSize <= 100, "page_size", "must be a maximum of 100")
	v.Check(validator.PermittedValue(f.Sort, f.SortSafelist...), "sort", "invalid sort value")
}

// Metadata is the standard pagination block returned alongside every list
// envelope. All fields omit when there are no records.
type Metadata struct {
	CurrentPage  int `json:"current_page,omitzero"`
	PageSize     int `json:"page_size,omitzero"`
	FirstPage    int `json:"first_page,omitzero"`
	LastPage     int `json:"last_page,omitzero"`
	TotalRecords int `json:"total_records,omitzero"`
}

func CalculateMetadata(totalRecords, page, pageSize int) Metadata {
	if totalRecords == 0 {
		return Metadata{}
	}

	return Metadata{
		CurrentPage:  page,
		PageSize:     pageSize,
		FirstPage:    1,
		LastPage:     (totalRecords + pageSize - 1) / pageSize,
		TotalRecords: totalRecords,
	}
}
