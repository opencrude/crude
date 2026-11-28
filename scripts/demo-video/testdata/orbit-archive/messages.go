package api

// Messages holds the user-facing API errors by language.
var Messages = map[string]map[string]string{
	"en": {
		"rate_limited": "Too many requests. Try again in %d seconds.",
		"denied":       "Access denied.",
	},
	"fa": {
		"rate_limited": "درخواست‌ها بیش از حد است. %d ثانیه دیگر دوباره امتحان کنید.",
		"denied":       "متأسفم دیو. متأسفانه نمی‌توانم این کار را بکنم.",
	},
	"ar": {
		"rate_limited": "طلبات كثيرة جدا. حاول مرة أخرى بعد %d ثانية.",
		"denied":       "تم رفض الوصول.",
	},
	"he": {
		"rate_limited": "יותר מדי בקשות. נסו שוב בעוד %d שניות.",
		"denied":       "הגישה נדחתה.",
	},
}
