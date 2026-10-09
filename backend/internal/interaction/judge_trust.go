package interaction

import (
	"fmt"
	"math"
	"time"
)

const (
	trustCatchFloor      = 0.80
	trustFalseAlarmCap   = 0.20
	trustHumanMargin     = 0.05
	trustMinimumPairs    = 20
	trustRecentWindow    = 14 * 24 * time.Hour
	trustZ95             = 1.96
	trustStatusTrusted   = "trusted"
	trustStatusCollected = "collecting"
	trustStatusBelow     = "below"
)

type TrustEstimate struct {
	Calls          int      `json:"calls"`
	Failures       int      `json:"failures"`
	Catch          *float64 `json:"catch"`
	CatchLow       *float64 `json:"catchLow"`
	FalseAlarms    *float64 `json:"falseAlarms"`
	FalseAlarmHigh *float64 `json:"falseAlarmHigh"`
	Agreement      *float64 `json:"agreement"`
	AgreementLow   *float64 `json:"agreementLow"`
	HumanAgreement *float64 `json:"humanAgreement"`
	HumanPairs     int      `json:"humanPairs"`
	Status         string   `json:"status"`
	Reason         string   `json:"reason"`
}

type trustCall struct {
	answers    map[string]bool
	judge      *bool
	sample     string
	date       string
	reviewedAt time.Time
}

type reviewPool struct{ flagged, random int }

type weightedRate struct{ hits, total, squares float64 }

func (rate *weightedRate) add(weight float64, hit bool) {
	rate.total += weight
	rate.squares += weight * weight
	if hit {
		rate.hits += weight
	}
}

func (rate weightedRate) value() *float64 {
	if rate.total == 0 {
		return nil
	}
	value := rate.hits / rate.total
	return &value
}

func (rate weightedRate) low() *float64 {
	if rate.total == 0 {
		return nil
	}
	low := wilsonLow(rate.hits/rate.total, rate.total*rate.total/rate.squares)
	return &low
}

func wilsonLow(p, n float64) float64 {
	if n <= 0 {
		return 0
	}
	z2 := trustZ95 * trustZ95
	center := p + z2/(2*n)
	margin := trustZ95 * math.Sqrt(p*(1-p)/n+z2/(4*n*n))
	return math.Max(0, (center-margin)/(1+z2/n))
}

func consensus(answers map[string]bool) (bool, bool) {
	seen := map[bool]bool{}
	var value bool
	for _, answer := range answers {
		seen[answer] = true
		value = answer
	}
	return value, len(seen) == 1
}

type trustRates struct{ catch, falseAlarm, agreement weightedRate }

func measureTrust(calls map[string]*trustCall, pools map[string]reviewPool, since time.Time) (trustRates, int, int) {
	sampled := map[string]int{}
	for _, call := range calls {
		if call.judge != nil && !call.reviewedAt.Before(since) {
			sampled[call.date+":"+call.sample]++
		}
	}
	rates := trustRates{}
	compared, failures := 0, 0
	for _, call := range calls {
		pool, pooled := pools[call.date]
		human, agreed := consensus(call.answers)
		if call.judge == nil || !pooled || !agreed || call.reviewedAt.Before(since) {
			continue
		}
		population := pool.random
		if call.sample == "flagged" {
			population = pool.flagged
		} else if call.sample != "random" {
			continue
		}
		weight := float64(population) / float64(sampled[call.date+":"+call.sample])
		if weight <= 0 {
			continue
		}
		compared++
		judge := *call.judge
		rates.agreement.add(weight, judge == human)
		if !human {
			failures++
			rates.catch.add(weight, !judge)
		}
		if !judge {
			rates.falseAlarm.add(weight, human)
		}
	}
	return rates, compared, failures
}

func estimateTrust(calls map[string]*trustCall, pools map[string]reviewPool, now time.Time) TrustEstimate {
	pairs, pairsAgreed := 0, 0
	for _, call := range calls {
		if len(call.answers) > 1 {
			pairs++
			if _, agreed := consensus(call.answers); agreed {
				pairsAgreed++
			}
		}
	}
	rates, compared, failures := measureTrust(calls, pools, time.Time{})
	estimate := TrustEstimate{
		Calls: compared, Failures: failures, HumanPairs: pairs,
		Catch: rates.catch.value(), CatchLow: rates.catch.low(),
		FalseAlarms: rates.falseAlarm.value(), Agreement: rates.agreement.value(), AgreementLow: rates.agreement.low(),
	}
	if rates.falseAlarm.total > 0 {
		high := 1 - wilsonLow(1-rates.falseAlarm.hits/rates.falseAlarm.total, rates.falseAlarm.total*rates.falseAlarm.total/rates.falseAlarm.squares)
		estimate.FalseAlarmHigh = &high
	}
	if pairs > 0 {
		human := float64(pairsAgreed) / float64(pairs)
		estimate.HumanAgreement = &human
	}
	recent, _, _ := measureTrust(calls, pools, now.Add(-trustRecentWindow))
	estimate.Status, estimate.Reason = trustVerdict(estimate, recent)
	return estimate
}

func trustVerdict(estimate TrustEstimate, recent trustRates) (string, string) {
	percent := func(value *float64) string { return fmt.Sprintf("%.0f%%", 100**value) }
	switch {
	case estimate.Failures == 0 || estimate.CatchLow == nil:
		return trustStatusCollected, "No reviewer-marked failures yet"
	case *estimate.Catch < trustCatchFloor:
		return trustStatusBelow, "Catches " + percent(estimate.Catch) + " of failures, needs 80%"
	case estimate.FalseAlarms != nil && *estimate.FalseAlarms > trustFalseAlarmCap:
		return trustStatusBelow, percent(estimate.FalseAlarms) + " of its no's are false alarms, max 20%"
	case estimate.HumanAgreement != nil && estimate.HumanPairs >= trustMinimumPairs && *estimate.Agreement < *estimate.HumanAgreement-trustHumanMargin:
		return trustStatusBelow, "Agrees " + percent(estimate.Agreement) + ", reviewers agree " + percent(estimate.HumanAgreement)
	case *estimate.CatchLow < trustCatchFloor:
		return trustStatusCollected, "Catch is at least " + percent(estimate.CatchLow) + " so far; more failures needed to show 80%"
	case estimate.FalseAlarmHigh != nil && *estimate.FalseAlarmHigh > trustFalseAlarmCap:
		return trustStatusCollected, "False alarms could be up to " + percent(estimate.FalseAlarmHigh) + "; more reviews needed"
	case estimate.HumanPairs < trustMinimumPairs:
		return trustStatusCollected, fmt.Sprintf("%d of %d shared reviews needed to measure reviewer agreement", estimate.HumanPairs, trustMinimumPairs)
	case *estimate.AgreementLow < *estimate.HumanAgreement-trustHumanMargin:
		return trustStatusCollected, "Agreement is at least " + percent(estimate.AgreementLow) + " so far, vs reviewers' " + percent(estimate.HumanAgreement)
	}
	if catch := recent.catch.value(); catch != nil && *catch < trustCatchFloor {
		return trustStatusBelow, "Last 14 days: catches " + percent(catch)
	}
	if falseAlarms := recent.falseAlarm.value(); falseAlarms != nil && *falseAlarms > trustFalseAlarmCap {
		return trustStatusBelow, "Last 14 days: " + percent(falseAlarms) + " false alarms"
	}
	if agreement := recent.agreement.value(); agreement != nil && *agreement < *estimate.HumanAgreement-trustHumanMargin {
		return trustStatusBelow, "Last 14 days: agrees " + percent(agreement)
	}
	return trustStatusTrusted, "Meets every bar at 95% confidence"
}
