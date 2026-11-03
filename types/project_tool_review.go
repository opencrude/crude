package types

// ProjectToolCandidate describes one proposed download or unsupported tool.
type ProjectToolCandidate struct {
	ID                             string
	Languages, ServerIDs           []string
	Package, Version, Source, URL  string
	File, Archive, Bin, Command    string
	Licenses                       []string
	SizeKnown                      bool
	SizeBytes                      int64
	ChecksumPublished, Installable bool
	Reason                         string
}

// ProjectToolReview is a deterministic preimage, not an approval.
type ProjectToolReview struct {
	Project, RootIdentity string
	AssemblyGeneration    uint64
	Items                 []ProjectToolCandidate
	Token                 string
}

// CloneProjectToolCandidate returns a detached copy.
func CloneProjectToolCandidate(item ProjectToolCandidate) ProjectToolCandidate {
	item.Languages = append([]string(nil), item.Languages...)
	item.ServerIDs = append([]string(nil), item.ServerIDs...)
	item.Licenses = append([]string(nil), item.Licenses...)
	return item
}

// CloneProjectToolReview returns a detached copy.
func CloneProjectToolReview(review ProjectToolReview) ProjectToolReview {
	if review.Items != nil {
		items := make([]ProjectToolCandidate, len(review.Items))
		for i, item := range review.Items {
			items[i] = CloneProjectToolCandidate(item)
		}
		review.Items = items
	}
	return review
}
