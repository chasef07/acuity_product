package knowledge

import (
	"strings"
	"unicode/utf8"
)

// An office corpus is small, so exact scoped vector comparison avoids ANN
// post-filter recall loss. Reciprocal rank fusion lets lexical matches promote
// names without comparing incompatible lexical and cosine score scales. The
// semantic floor still applies: a shared word alone cannot establish relevance.
// A complete multiword title match can rescue named entries when their semantic
// score is low.
const hybridSearchSQL = `
WITH scoped AS (
 SELECT *,tsvector_to_array(search_document) AS terms FROM knowledge_passages WHERE revision_id=$1
), query_terms AS (
 SELECT unnest(tsvector_to_array(to_tsvector('english',$4))) AS term
), query_coverage AS (
 SELECT coalesce(avg(CASE WHEN EXISTS(SELECT 1 FROM scoped WHERE q.term=ANY(terms)) THEN 1.0 ELSE 0 END),0) AS fraction
 FROM query_terms q
), distinctive AS (
 SELECT q.term FROM query_terms q JOIN scoped s ON q.term=ANY(s.terms)
 GROUP BY q.term HAVING count(*)<=greatest(2,(SELECT count(*) FROM scoped)/5)
), eligible AS (
 SELECT section_id,title,text,embedding <=> $2::vector AS distance,
 ARRAY(SELECT term FROM distinctive WHERE term=ANY(terms)) AS matched_terms,
 ARRAY(SELECT term FROM distinctive WHERE term=ANY(
   tsvector_to_array(to_tsvector('english',array_to_string(ARRAY(
    SELECT unnest(tsvector_to_array(to_tsvector('simple',title)))
    INTERSECT SELECT unnest(tsvector_to_array(to_tsvector('simple',$4)))
   ),' '))))) AS title_terms,
 ts_rank_cd(to_tsvector('english',title),
   replace(plainto_tsquery('english',$4)::text,' & ',' | ')::tsquery,2) +
 0.1*ts_rank_cd(to_tsvector('english',text),
   replace(plainto_tsquery('english',$4)::text,' & ',' | ')::tsquery,2) AS lexical
 FROM scoped
 WHERE (1-(embedding <=> $2::vector)>=$3 OR (
 array_length(regexp_split_to_array(trim(title), '\s+'),1)>=2
 AND to_tsvector('english',$4) @@ phraseto_tsquery('english',title)
 ))
), ranked AS (
 SELECT *,row_number() OVER (ORDER BY distance,section_id) AS semantic_rank,
 row_number() OVER (ORDER BY lexical DESC,section_id) AS lexical_rank
 FROM eligible
)
SELECT section_id,title,text,1-distance,lexical,matched_terms,title_terms,(SELECT fraction FROM query_coverage) FROM ranked
ORDER BY (1.0/(60+semantic_rank) +
 CASE WHEN lexical>0 THEN 1.0/(60+lexical_rank) ELSE 0 END) DESC,
 distance,section_id LIMIT 24`

// Return complete evidence, never a substring that might lose an exception.
// A legacy section larger than the budget is returned alone until republished
// as focused entries. The budget bounds evidence text, not JSON serialization.
const responseTextBudget = 3000

type searchCandidate struct {
	Passage
	similarity    float64
	lexical       float64
	matchedTerms  []string
	titleTerms    []string
	queryCoverage float64
}

// Select enough evidence to cover the query's distinctive terms. Corpus-common
// vocabulary cannot keep adding unrelated office entries. Stemming is performed
// by PostgreSQL, and all candidates still satisfy the retrieval relevance gate.
// Normalized lexical score breaks equal-coverage ties, followed by hybrid rank.
// Unfamiliar vocabulary and semantic-only paraphrases preserve bounded candidates:
// English lexical coverage cannot establish sufficiency for those queries.
func relevantPassages(candidates []searchCandidate) []Passage {
	selected := []Passage{}
	if len(candidates) > 0 && candidates[0].queryCoverage < 0.5 {
		for _, candidate := range candidates {
			selected = append(selected, candidate.Passage)
		}
		return selected
	}
	covered := map[string]bool{}
	used := make([]bool, len(candidates))
	for {
		best, gain := -1, 0
		for i, c := range candidates {
			if used[i] {
				continue
			}
			added := 0
			for _, term := range c.matchedTerms {
				if !covered[term] {
					added++
				}
			}
			titleAdded := 0
			for _, term := range c.titleTerms {
				if !covered[term] {
					titleAdded++
				}
			}
			if added < 2 && titleAdded == 0 {
				continue
			}
			if added > gain || (added == gain && best >= 0 && c.lexical > candidates[best].lexical) {
				best, gain = i, added
			}
		}
		if best < 0 {
			break
		}
		selected = append(selected, candidates[best].Passage)
		used[best] = true
		for _, term := range candidates[best].matchedTerms {
			covered[term] = true
		}
	}
	if len(selected) == 0 && len(candidates) > 0 {
		for _, candidate := range candidates {
			selected = append(selected, candidate.Passage)
		}
	}
	return selected
}

func selectPassages(candidates []Passage, limit int) []Passage {
	selected := make([]Passage, 0, limit)
	seen := make(map[string]bool)
	characters := 0
	for _, p := range candidates {
		key := strings.Join(strings.Fields(p.Text), " ")
		if seen[key] {
			continue
		}
		size := utf8.RuneCountInString(p.Text) + utf8.RuneCountInString(p.Title)
		if len(selected) > 0 && characters+size > responseTextBudget {
			// Do not replace stronger evidence with a smaller, weaker result.
			break
		}
		selected = append(selected, p)
		seen[key] = true
		characters += size
		if len(selected) == limit || characters >= responseTextBudget {
			break
		}
	}
	return selected
}
