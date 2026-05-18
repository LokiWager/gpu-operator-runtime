package serverless

import (
	"fmt"
	"regexp"
	"strings"
)

var subjectTokenPattern = regexp.MustCompile(`^[a-z0-9_-]+$`)
var invalidSubjectTokenPattern = regexp.MustCompile(`[^a-z0-9_-]+`)

// NormalizeSubjectPrefix trims, defaults, and strips trailing separators from a subject prefix.
func NormalizeSubjectPrefix(value string) string {
	trimmed := strings.Trim(strings.TrimSpace(value), ".")
	if trimmed == "" {
		return DefaultSubjectPrefix
	}
	return trimmed
}

// NormalizeRequestID trims, lowercases, and validates one request identifier as a single NATS subject token.
func NormalizeRequestID(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return "", fmt.Errorf("serverlessRequestID is required")
	}
	if !subjectTokenPattern.MatchString(normalized) {
		return "", fmt.Errorf("serverlessRequestID %q is invalid; use only lowercase letters, numbers, hyphens, or underscores", normalized)
	}
	return normalized, nil
}

// InvocationSubject returns the NATS subject carrying queued invocation messages.
func InvocationSubject(prefix, requestID string) string {
	return fmt.Sprintf("%s.invoke.%s", NormalizeSubjectPrefix(prefix), requestID)
}

// ResultSubject returns the NATS subject carrying invocation completion events.
func ResultSubject(prefix, requestID string) string {
	return fmt.Sprintf("%s.result.%s", NormalizeSubjectPrefix(prefix), requestID)
}

// DispatchSubject returns the NATS subject carrying worker-targeted dispatch messages for one ready worker sidecar.
func DispatchSubject(prefix, requestID, workerName string) string {
	return fmt.Sprintf("%s.dispatch.%s.%s", NormalizeSubjectPrefix(prefix), normalizeDispatchToken(requestID), normalizeDispatchToken(workerName))
}

// MetricsSubject returns the NATS subject carrying worker metrics and lifecycle events.
func MetricsSubject(prefix, requestID string) string {
	return fmt.Sprintf("%s.metrics.%s", NormalizeSubjectPrefix(prefix), requestID)
}

// StreamSubjects returns the wildcard subject bindings required for the chapter's queue-first ingress stream.
func StreamSubjects(prefix string) []string {
	base := NormalizeSubjectPrefix(prefix)
	return []string{
		fmt.Sprintf("%s.invoke.*", base),
		fmt.Sprintf("%s.dispatch.*.*", base),
		fmt.Sprintf("%s.result.*", base),
		fmt.Sprintf("%s.metrics.*", base),
	}
}

func normalizeDispatchToken(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, ".", "-")
	if normalized == "" {
		return "unknown"
	}
	if subjectTokenPattern.MatchString(normalized) {
		return normalized
	}
	return invalidSubjectTokenPattern.ReplaceAllString(normalized, "-")
}
