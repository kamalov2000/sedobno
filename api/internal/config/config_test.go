package config

import (
	"strings"
	"testing"
)

// requiredEnv — минимальный набор валидного окружения для тестов.
func requiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("ADMIN_KEY", "admin")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "whsecret")
	t.Setenv("FEEDBACK_CHAT_ID", "555999")
	t.Setenv("GROQ_API_KEY", "gsk_test")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test") // дефолтный провайдер — anthropic
	t.Setenv("SUBSCRIPTION_STARS_PRICE", "150")
}

func TestMustLoad_OK(t *testing.T) {
	requiredEnv(t)
	cfg := MustLoad()
	if cfg.Port != "8080" {
		t.Errorf("Port по умолчанию = %q, ждали 8080", cfg.Port)
	}
	if cfg.LLMProvider != "anthropic" {
		t.Errorf("LLMProvider по умолчанию = %q, ждали anthropic", cfg.LLMProvider)
	}
	if cfg.SubscriptionStarsPrice != 150 {
		t.Errorf("SubscriptionStarsPrice = %d, ждали 150", cfg.SubscriptionStarsPrice)
	}
}

func TestMustLoad_PanicsOnMissing(t *testing.T) {
	// Пустое окружение: должны упасть и перечислить все недостающие переменные.
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SECRET", "")
	t.Setenv("ADMIN_KEY", "")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "")
	t.Setenv("FEEDBACK_CHAT_ID", "")
	t.Setenv("GROQ_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "") // дефолтный провайдер anthropic → его ключ обязателен
	t.Setenv("SUBSCRIPTION_STARS_PRICE", "")

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("ожидали панику при отсутствии обязательных переменных")
		}
		msg, _ := r.(string)
		for _, key := range []string{"DATABASE_URL", "JWT_SECRET", "ADMIN_KEY", "TELEGRAM_BOT_TOKEN", "TELEGRAM_WEBHOOK_SECRET", "FEEDBACK_CHAT_ID", "ANTHROPIC_API_KEY", "SUBSCRIPTION_STARS_PRICE"} {
			if !strings.Contains(msg, key) {
				t.Errorf("сообщение паники не упоминает %s: %s", key, msg)
			}
		}
	}()
	MustLoad()
}

func TestMustLoad_InvalidPrice(t *testing.T) {
	requiredEnv(t)
	t.Setenv("SUBSCRIPTION_STARS_PRICE", "0")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("ожидали панику при SUBSCRIPTION_STARS_PRICE=0")
		}
	}()
	MustLoad()
}

// По умолчанию СБП выключен: ключи не требуются, цена 249, allowlist распарсен.
func TestMustLoad_SBPDisabledByDefault(t *testing.T) {
	requiredEnv(t)
	cfg := MustLoad()
	if cfg.SBPEnabled {
		t.Error("SBPEnabled по умолчанию должен быть false")
	}
	if cfg.SBPPriceRub != 249 {
		t.Errorf("SBPPriceRub = %d, ждали 249", cfg.SBPPriceRub)
	}
	if len(cfg.YooKassaAllowlist) == 0 {
		t.Error("YooKassaAllowlist пуст — дефолтные сети не распарсились")
	}
}

// Включённый СБП без ключей/лендинга — паника с перечислением недостающих (fail-fast §11).
func TestMustLoad_SBPEnabledRequiresKeys(t *testing.T) {
	requiredEnv(t)
	t.Setenv("SBP_ENABLED", "true")
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("ожидали панику: SBP_ENABLED=true без ключей ЮKassa")
		}
		msg, _ := r.(string)
		for _, key := range []string{"YOOKASSA_SHOP_ID", "YOOKASSA_SECRET_KEY", "LANDING_URL", "LANDING_LINK_SECRET"} {
			if !strings.Contains(msg, key) {
				t.Errorf("паника не упоминает %s: %s", key, msg)
			}
		}
	}()
	MustLoad()
}

// Ключ обязателен у ВЫБРАННОГО провайдера (fail-fast §11), а не безусловно у groq.
func TestMustLoad_RequiresSelectedProviderKey(t *testing.T) {
	requiredEnv(t)
	t.Setenv("LLM_PROVIDER", "groq")
	t.Setenv("GROQ_API_KEY", "")
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("ожидали панику: провайдер groq без GROQ_API_KEY")
		}
		if msg, _ := r.(string); !strings.Contains(msg, "GROQ_API_KEY") {
			t.Errorf("паника не упоминает GROQ_API_KEY: %s", msg)
		}
	}()
	MustLoad()
}
