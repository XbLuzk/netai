package server

import (
	"net/http"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

func RequestID(next http.Handler) http.Handler {
	return chimiddleware.RequestID(next)
}

func Logging(next http.Handler) http.Handler {
	return chimiddleware.Logger(next)
}

func Recovery(next http.Handler) http.Handler {
	return chimiddleware.Recoverer(next)
}
