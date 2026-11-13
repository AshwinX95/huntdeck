package store

// Whether a program's 90-day figures are estimates, one rule for every
// page and the API: estimated where the figure is one by how it's worked
// out, or where it's counted from fewer than 90 days of saved totals.

// fullCount is how many days of saved totals a figure counted from them
// needs to be exact (syncer.countedDays).
const fullCount = 90

// Paid90dIsEstimate reports whether Paid90d is an estimate: Bugcrowd's
// payable accepted reports × average payout (Paid90dEstimated), its rise
// in rewarded reports × average payout (counted, but always an estimate),
// or a total paid counted over fewer than 90 days of saved totals
// (Intigriti).
func (it ProgramListItem) Paid90dIsEstimate() bool {
	if it.Paid90dEstimated {
		return true
	}
	return it.CountedDays != nil && (*it.CountedDays < fullCount || it.Platform == "bugcrowd")
}

// Reports90dIsEstimate reports whether Reports90d is an estimate: counted
// from fewer than 90 days of saved report totals (YesWeHack, Intigriti).
func (it ProgramListItem) Reports90dIsEstimate() bool {
	return it.Reports90d != nil && it.CountedDays != nil && *it.CountedDays < fullCount
}
