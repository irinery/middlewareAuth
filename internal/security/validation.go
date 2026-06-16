package security

import "regexp"

var (
	projectIDRe  = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)
	profileIDRe  = regexp.MustCompile(`^[a-zA-Z0-9_@.:-]{1,120}$`)
	originatorRe = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)
)

func ValidProjectID(projectID string) bool {
	return projectIDRe.MatchString(projectID)
}

func ValidProfileID(profileID string) bool {
	return profileIDRe.MatchString(profileID)
}

func ValidOriginator(originator string) bool {
	return originator == "" || originatorRe.MatchString(originator)
}

func NormalizeProfileID(profileID string) string {
	if profileID == "" {
		return "default"
	}
	return profileID
}
