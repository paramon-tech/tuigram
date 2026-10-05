package core

import "context"

// ManagementClient optionally supports editing contacts and conversations.
// Deleting a private chat removes history only for the current account. Deleting
// a group or channel leaves it; it must never delete the chat for other members.
type ManagementClient interface {
	// CreateGroup can return a created Chat together with a warning error if
	// Telegram refused some invitations. Callers must not retry creation then.
	CreateGroup(context.Context, string, []Contact) (Chat, error)
	RenameChat(context.Context, Chat, string) error
	DeleteChat(context.Context, Chat) error
	EditContact(context.Context, Contact, string) error
	DeleteContact(context.Context, Contact) error
}

// ContactImporter adds a contact using an international phone number and name.
type ContactImporter interface {
	ImportContact(context.Context, string, string) (Contact, error)
}
