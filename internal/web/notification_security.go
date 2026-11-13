package web

import "household/internal/i18n"

func securityNoticeTitle(locale, kind string) string {
	switch kind {
	case "security_password_changed":
		return i18n.Text(locale, "Dein Passwort wurde geändert.")
	case "security_totp_enabled":
		return i18n.Text(locale, "Die Zwei-Faktor-Anmeldung wurde aktiviert.")
	case "security_totp_disabled":
		return i18n.Text(locale, "Die Zwei-Faktor-Anmeldung wurde deaktiviert.")
	case "security_recovery_codes_regenerated":
		return i18n.Text(locale, "Neue Wiederherstellungscodes wurden erstellt.")
	case "security_recovery_code_used":
		return i18n.Text(locale, "Ein Wiederherstellungscode wurde verwendet.")
	case "security_passkey_added":
		return i18n.Text(locale, "Ein Passkey wurde hinzugefügt.")
	case "security_passkey_removed":
		return i18n.Text(locale, "Ein Passkey wurde entfernt.")
	case "security_sessions_revoked":
		return i18n.Text(locale, "Angemeldete Sitzungen wurden beendet.")
	case "security_account_changed":
		return i18n.Text(locale, "Die Berechtigungen oder der Status deines Kontos wurden geändert.")
	case "security_device_authorized":
		return i18n.Text(locale, "Ein Gerät wurde für Benachrichtigungen freigegeben.")
	default:
		return i18n.Text(locale, "Sicherheitsereignis")
	}
}
