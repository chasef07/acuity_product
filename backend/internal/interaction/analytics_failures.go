package interaction

// Version and question pairs keep removed or contextual judges from becoming failures.
func judgeFailureKeys(judge string) []string {
	keys := []string{}
	for _, version := range []string{ScorecardJudgeVersion, legacyJudgeVersion, previousJudgeVersion} {
		for _, question := range ScorecardQuestionsForVersion(version) {
			if question.Source == "judge" && question.Failure && (judge == "" || question.Key == judge) {
				keys = append(keys, version+":"+question.Key)
			}
		}
	}
	return keys
}
