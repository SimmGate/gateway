package types

type ChatRequest struct {
	Model string `json:"model"`
}

type ChatResponse struct {
	Message string `json:"message"`
}
