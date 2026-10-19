package service

import (
	"encoding/json"
	"strconv"
	"strings"
)

func normalizeOnboardingPlan(value string) string {
	key := strings.ToLower(strings.TrimSpace(value))
	key = strings.NewReplacer("-", "", "_", "", " ", "").Replace(key)
	switch key {
	case "chatgptplus", "plus":
		return "plus"
	case "prolite", "pro5x":
		return "pro_5x"
	case "chatgptpro", "pro", "pro20x":
		return "pro"
	case "promax", "pro50x":
		return "pro_max"
	case "businesspremium", "selfservebusinessprolite":
		return "business_premium"
	case "businessstandard", "team", "selfservebusinessusagebased":
		return "business_standard"
	default:
		return key
	}
}

func onboardingPlanUnits(plan string) int {
	switch normalizeOnboardingPlan(plan) {
	case "plus", "business_standard":
		return 1
	case "pro_5x", "business_premium":
		return 5
	case "pro":
		return 20
	case "pro_max":
		return 50
	default:
		return 0
	}
}

func onboardingPlanName(plan string) string {
	switch normalizeOnboardingPlan(plan) {
	case "plus":
		return "Plus"
	case "business_standard":
		return "Business Standard"
	case "business_premium":
		return "Business Premium"
	case "pro_5x":
		return "Pro 5x"
	case "pro":
		return "Pro 20x"
	case "pro_max":
		return "Pro 50x"
	default:
		return plan
	}
}

func selectOnboardingSubscription(units int, rules map[string]OnboardingSubscriptionRule) map[int64]OnboardingSubscriptionRule {
	selected := make(map[int64]OnboardingSubscriptionRule)
	if units > 5 {
		units = 5
	}
	if rule, ok := rules[strconv.Itoa(units)]; units > 0 && ok && rule.GroupID > 0 {
		selected[rule.GroupID] = rule
	}
	return selected
}

func normalizeOnboardingSubscriptionRules(raw []byte, config *OnboardingConfig) error {
	var legacy struct {
		Rules map[string]struct {
			OnboardingSubscriptionRule
			Units int `json:"units"`
		} `json:"subscription_rules"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &legacy); err != nil {
			return err
		}
	}
	rules := make(map[string]OnboardingSubscriptionRule)
	conflicts := make(map[string]bool)
	for key, old := range legacy.Rules {
		if key == "1" || key == "2" || key == "3" || key == "4" || key == "5" {
			rules[key] = old.OnboardingSubscriptionRule
		}
	}
	for key, old := range legacy.Rules {
		if key == "1" || key == "2" || key == "3" || key == "4" || key == "5" || old.GroupID == 0 {
			continue
		}
		units := old.Units
		if units <= 0 {
			units = onboardingPlanUnits(key)
		}
		if units <= 0 {
			config.SubscriptionRulesNeedReview = true
			continue
		}
		if units > 5 {
			units = 5
		}
		tier := strconv.Itoa(units)
		if current, exists := rules[tier]; exists && current != old.OnboardingSubscriptionRule {
			conflicts[tier] = true
			config.SubscriptionRulesNeedReview = true
		}
		rules[tier] = old.OnboardingSubscriptionRule
	}
	for tier := range conflicts {
		delete(rules, tier)
	}
	config.SubscriptionRules = rules
	return nil
}
