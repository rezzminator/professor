package ui

// pinScheduleFiredRows emits every fired, visible row ahead of every other row
// (update and new-chat pins, project and name groups) in project-group order,
// flat: such a row never joins a name group. It pins from every place: a
// schedule exists to be seen, wherever its chat runs.
func (model *Model) pinScheduleFiredRows(pinned map[int]bool) {
	for _, group := range model.groups {
		for _, index := range group.indices {
			if model.rows[index].ScheduleFired && model.visibleInView(model.rows[index]) {
				model.order = append(model.order, index)
				pinned[index] = true
			}
		}
	}
}
