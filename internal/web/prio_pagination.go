package web

import (
	"net/url"
	"strconv"
)

const prioOverviewPageSize = 25

type prioOverviewPage struct {
	Number, Count      int
	PrevHref, NextHref string
}

func paginatePrioOverview(q url.Values, groups []prioGroup, ticketCount int) prioOverviewPage {
	count := max(1, (ticketCount+prioOverviewPageSize-1)/prioOverviewPageSize)
	requested, _ := strconv.Atoi(q.Get("page"))
	number := min(max(1, requested), count)
	q.Del("page")
	if number > 1 {
		q.Set("page", strconv.Itoa(number))
	}
	start := (number - 1) * prioOverviewPageSize
	end := start + prioOverviewPageSize
	offset := 0
	for i := range groups {
		group := &groups[i]
		group.Total = len(group.Rows)
		first, last := max(0, start-offset), min(group.Total, end-offset)
		offset += group.Total
		if first >= last {
			group.Rows = nil
			continue
		}
		group.Rows = group.Rows[first:last]
		for j := range group.Rows {
			row := &group.Rows[j]
			row.SelectHref = prioHref(q, "ticket", row.Key)
		}
	}
	page := prioOverviewPage{Number: number, Count: count}
	if number > 1 {
		previous := ""
		if number > 2 {
			previous = strconv.Itoa(number - 1)
		}
		page.PrevHref = prioHref(q, "page", previous)
	}
	if number < count {
		page.NextHref = prioHref(q, "page", strconv.Itoa(number+1))
	}
	return page
}
