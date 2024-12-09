package common

type LanguageName string

const (
	Golang     LanguageName = "golang"
	TypeScript LanguageName = "typescript"
	Vue        LanguageName = "vue"
)

func StringToLanguageName(lang string) LanguageName {
	switch lang {
	case "golang":
		return Golang
	case "typescript":
		return TypeScript
	case "vue":
		return Vue
	default:
		return LanguageName("unknown")
	}
}
