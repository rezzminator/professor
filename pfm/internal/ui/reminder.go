package ui

// pinRemindedRows emits every reminded, visible row ahead of every other row
// (update and new-chat pins, project and name groups) in project-group order,
// flat: such a row never joins a name group.
func (model *Model) pinRemindedRows(pinned map[int]bool) {
	for _, group := range model.groups {
		for _, index := range group.indices {
			if model.rows[index].Reminded && model.visibleInView(model.rows[index]) {
				model.order = append(model.order, index)
				pinned[index] = true
			}
		}
	}
}
