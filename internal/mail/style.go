package mail

import "git.nite07.com/nite/engram/internal/mailstyle"

// This file is the mail package's view of the inline styles of a mail body.
//
// The values live in internal/mailstyle because two packages need them: the built-in bodies below
// write them into their HTML, and the store's one-time template migration uses them to convert stored
// Markdown templates. Package mail only needs the button style to assert that a call-to-action mail
// renders a button.
const buttonStyle = mailstyle.Button
