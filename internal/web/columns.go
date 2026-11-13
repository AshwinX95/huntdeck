package web

// A column picker lets a table's columns be shown, hidden and reordered
// (static/columns.js; the menu is layout.html's "colpick"). The table
// carries data-cols with the picker's Name and a c-<key> class on each of
// its header and body cells; the choices are kept in the browser, under
// keys made from the Name. The Programs list and the hunt board's fresh
// targets use it.
type columnPicker struct {
	Name    string        // the table's data-cols, and the browser's storage keys
	Label   string        // the button's label
	Icon    string        // the button's icon, from layout.html's sprite
	Fixed   string        // the column that always shows, first
	Columns []tableColumn // the rest, in the table's default order
}

// tableColumn is one column, by its c-* class. A group is columns under
// one heading (a th with data-grp listing them): they move together and
// reorder among themselves.
type tableColumn struct {
	Key, Label string
	Group      []tableColumn
}
