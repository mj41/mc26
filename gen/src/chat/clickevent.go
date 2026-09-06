package chat

// ClickEvent (style_gen.go) is what happens when a text component is clicked:
// its Action selects the case and the case's field carries the argument.

// OpenURL opens the given URL in the default web browser.
func OpenURL(url string) *ClickEvent {
	return &ClickEvent{Action: ClickEventActionOpenURL, URL: url}
}

// RunCommand runs the given command as the player who clicked.
func RunCommand(cmd string) *ClickEvent {
	return &ClickEvent{Action: ClickEventActionRunCommand, Command: cmd}
}

// SuggestCommand puts the given text into the player's chat box.
func SuggestCommand(cmd string) *ClickEvent {
	return &ClickEvent{Action: ClickEventActionSuggestCommand, Command: cmd}
}

// ChangePage turns a written book to the given page.
func ChangePage(page int) *ClickEvent {
	return &ClickEvent{Action: ClickEventActionChangePage, Page: int32(page)}
}

// CopyToClipboard copies the given text to the player's clipboard.
func CopyToClipboard(text string) *ClickEvent {
	return &ClickEvent{Action: ClickEventActionCopyToClipboard, Value: text}
}
