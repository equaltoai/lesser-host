package hostedgenesis

import "strings"

// The current candidate must prove the exact accepted-tool transition.
func declarationProviderContinuationBound(candidate *DeclarationCandidate, update DeclarationProviderAttemptUpdate) bool {
	if !providerContinuationPhaseBound(candidate, update) {
		return false
	}
	toolName, ok := DeclarationToolForSection(update.Section)
	if !ok {
		return false
	}
	for _, attempt := range candidate.ProviderAttempts {
		if !providerContinuationAttemptBound(attempt, update, toolName) {
			continue
		}
		for _, record := range candidate.ToolRecords {
			if providerContinuationToolBound(record, attempt, candidate) {
				return true
			}
		}
	}
	return false
}

func providerContinuationPhaseBound(candidate *DeclarationCandidate, update DeclarationProviderAttemptUpdate) bool {
	if update.Section == DeclarationSectionSoul || candidate.Revision != update.CandidateRevision+1 {
		return false
	}
	switch candidate.Phase {
	case DeclarationCandidatePhaseSection:
		return candidate.CurrentSection != "" && candidate.CurrentSection != update.Section
	case DeclarationCandidatePhaseReview:
		return candidate.CurrentSection == "" && candidate.Review != nil
	default:
		return false
	}
}

func providerContinuationAttemptBound(attempt DeclarationProviderAttempt, update DeclarationProviderAttemptUpdate, toolName string) bool {
	return attempt.SourceTurnID == update.SourceTurnID && attempt.Section == update.Section &&
		attempt.CandidateRevision == update.CandidateRevision && attempt.CandidateHash == update.CandidateHash &&
		attempt.Provider == strings.ToLower(strings.TrimSpace(update.Provider)) && attempt.Model == strings.TrimSpace(update.Model) &&
		attempt.Phase == update.Phase && attempt.Accepted && attempt.ToolName == toolName && attempt.ToolCallHash != ""
}

func providerContinuationToolBound(record DeclarationToolRecord, attempt DeclarationProviderAttempt, candidate *DeclarationCandidate) bool {
	return record.ToolCallHash == attempt.ToolCallHash && record.ToolName == attempt.ToolName &&
		record.Section == attempt.Section && record.SourceTurnID == attempt.SourceTurnID &&
		record.Revision == candidate.Revision && record.CandidateHash == candidate.CandidateHash &&
		record.SectionHash == candidate.SectionHashes[string(attempt.Section)]
}
