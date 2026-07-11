package api

import (
	"net/http"
	"strconv"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

type pagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	TotalCount int `json:"total_count"`
}

// parsePagination reads ?page and ?page_size. Values that are not positive
// integers come back as problems (respond 422); an oversized page_size is
// clamped to maxPageSize rather than rejected.
func parsePagination(r *http.Request) (pagination, map[string]string) {
	problems := map[string]string{}
	page := parsePositiveInt(r, "page", 1, problems)
	pageSize := parsePositiveInt(r, "page_size", defaultPageSize, problems)
	if len(problems) > 0 {
		return pagination{}, problems
	}
	return pagination{Page: page, PageSize: min(pageSize, maxPageSize)}, nil
}

func (p pagination) limit() int  { return p.PageSize }
func (p pagination) offset() int { return (p.Page - 1) * p.PageSize }

func parsePositiveInt(r *http.Request, name string, fallback int, problems map[string]string) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 {
		problems[name] = "must be a positive integer"
		return fallback
	}
	return v
}
