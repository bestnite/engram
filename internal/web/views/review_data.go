package views

// ReviewCardView 是当前卡片的两面渲染结果与标识。
// FrontHTML / BackHTML 一定来自 internal/render 的白名单清洗，SPA 可安全地按原样嵌入。
type ReviewCardView struct {
	CardID string
	NoteID string
	DeckID string
	// ExpectedVersion 是提交时携带的乐观锁版本（新卡为 "0"）。
	ExpectedVersion string
	Template        string
	FrontHTML       string
	BackHTML        string
	// EditHref 指向该 note 的编辑页，供 e 键跳转。
	EditHref string
}
