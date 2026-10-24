// Package chat is the example LLM feature. A user asks one question, and
// the answer streams back as Server-Sent Events. Without JavaScript, the
// form posts normally and the page shows the complete answer.
//
// The feature keeps no data, so it has no repository.go and no
// postgres.go. Add them when the feature must store conversations.
package chat

// ValidationError is rejected user input. Its text is safe to show to
// users, so handlers send it directly to the client.
type ValidationError string

// Error implements the error interface.
func (e ValidationError) Error() string { return string(e) }
