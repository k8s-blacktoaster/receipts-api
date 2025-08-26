package models

import "time"

type BaseResponse struct {
	Status    string    `json:"status"`
	Code      int       `json:"code"`
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type SuccessResponse struct {
	BaseResponse
	Data interface{} `json:"data,omitempty"`
}

type ErrorResponse struct {
	BaseResponse
}


type PaginatedResponse struct {
	BaseResponse
	Data       interface{}     `json:"data,omitempty"`
	Pagination *PaginationMeta `json:"pagination,omitempty"`
}

type PaginationMeta struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	Total  int `json:"total"`
}

func NewSuccessResponse(code int, message string, data interface{}) *SuccessResponse {
	return &SuccessResponse{
		BaseResponse: BaseResponse{
			Status:    "success",
			Code:      code,
			Message:   message,
			Timestamp: time.Now().UTC(),
		},
		Data: data,
	}
}

func NewErrorResponse(code int, message string) *ErrorResponse {
	return &ErrorResponse{
		BaseResponse: BaseResponse{
			Status:    "error",
			Code:      code,
			Message:   message,
			Timestamp: time.Now().UTC(),
		},
	}
}

func NewPaginatedResponse(code int, message string, data interface{}, pagination *PaginationMeta) *PaginatedResponse {
	return &PaginatedResponse{
		BaseResponse: BaseResponse{
			Status:    "success",
			Code:      code,
			Message:   message,
			Timestamp: time.Now().UTC(),
		},
		Data:       data,
		Pagination: pagination,
	}
}
