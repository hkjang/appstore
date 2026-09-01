package httpapi

import (
	"errors"
	"net/http"

	"github.com/hkjang/appstore/internal/store"
)

func storeError(err error, notFoundCode, notFoundMessage string) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return &APIError{Status: http.StatusNotFound, Code: notFoundCode, Message: notFoundMessage}
	case errors.Is(err, store.ErrConflict):
		return &APIError{Status: http.StatusConflict, Code: "CONFLICT", Message: "같은 값이 이미 존재하거나 현재 상태에서 처리할 수 없습니다."}
	case errors.Is(err, store.ErrForbidden):
		return Forbidden("이 작업을 수행할 수 없습니다.")
	case errors.Is(err, store.ErrInvalid):
		return Validation("입력값을 확인해 주세요.", nil)
	default:
		return err
	}
}
