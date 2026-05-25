package session

type UserInput struct {
	Text        string
	ReferenceIDs []string
}

func PlainInput(text string) UserInput {
	return UserInput{Text: text}
}

func InputWithRefs(text string, refIDs []string) UserInput {
	return UserInput{Text: text, ReferenceIDs: refIDs}
}
