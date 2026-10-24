// Package triage is the example System One feature. A user pastes a
// support message. A System One model picks the queue of the message and
// tells how probable it is that the message is urgent. The service turns
// the probabilities into a decision. When the model is not sure, a person
// must review the message.
//
// The feature keeps no data, so it has no repository.go and no
// postgres.go. Add them when the feature must store its decisions.
package triage

import "errors"

// ErrInvalidAnswer means that the model answer does not match the
// questions, for example an unknown option or a probability outside 0..1.
var ErrInvalidAnswer = errors.New("the model returned an invalid answer")

// ValidationError is rejected user input. Its text is safe to show to
// users, so handlers render it directly in forms.
type ValidationError string

// Error implements the error interface.
func (e ValidationError) Error() string { return string(e) }

// Queue names the team that handles a message.
type Queue string

// The queues. The model picks one of the first three. QueueReview is the
// decision of the service when the model is not sure.
const (
	QueueBilling Queue = "billing"
	QueueBug     Queue = "bug"
	QueueAccount Queue = "account"
	QueueReview  Queue = "review"
)

// Decision is the result of one triage.
type Decision struct {
	Queue  Queue
	Urgent bool
	// Confidence is the confidence of the model in its queue, from 0 to 1.
	Confidence float64
	// Urgency is the probability that the message is urgent, from 0 to 1.
	Urgency float64
	// Model is the exact model version that answered. Keep it with a
	// stored decision, so you can find the decisions of a bad version.
	Model string
}
