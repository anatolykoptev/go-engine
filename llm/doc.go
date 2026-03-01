// Package llm provides an OpenAI-compatible LLM client with retry
// and API key fallback rotation.
//
// Supports any OpenAI-compatible endpoint (CLIProxyAPI, Gemini, etc.)
// with automatic key rotation on quota exhaustion.
package llm
