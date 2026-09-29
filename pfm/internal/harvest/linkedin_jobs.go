package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The LinkedIn job view (/jobs/view/<slug-id>). Its entity is a JobPosting;
// the markup beside it holds more — the criteria list, the salary as shown
// and the applicant count — so the extractor reads those from it, the
// JSON-LD's baseSalary and industries standing in where the page shows none.

// linkedInBreakRuns is a run of hard breaks (<br><br>) in a job description,
// which parts paragraphs.
var linkedInBreakRuns = regexp.MustCompile(`(?: {2}\n){2,}`)

// ldCredential is a job's education requirement: its credentialCategory, or
// the requirement as plain text.
type ldCredential string

func (credential *ldCredential) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*credential = ldCredential(strings.TrimSpace(text))
		return nil
	}
	var object struct {
		Category ldText `json:"credentialCategory"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return fmt.Errorf("read a JSON-LD education requirement: %w", err)
	}
	*credential = ldCredential(object.Category)
	return nil
}

// ldSalary is a JobPosting's baseSalary: a MonetaryAmount whose value is a
// number or a QuantitativeValue.
type ldSalary struct {
	Currency ldText          `json:"currency"`
	Value    json.RawMessage `json:"value"`
}

type ldJob struct {
	Title              ldText               `json:"title"`
	Description        ldText               `json:"description"`
	DatePosted         ldText               `json:"datePosted"`
	ValidThrough       ldText               `json:"validThrough"`
	EmploymentType     ldList[ldText]       `json:"employmentType"`
	HiringOrganization ldThing              `json:"hiringOrganization"`
	JobLocation        ldList[ldPlace]      `json:"jobLocation"`
	Skills             ldList[ldText]       `json:"skills"`
	Industry           ldList[ldText]       `json:"industry"`
	Education          ldList[ldCredential] `json:"educationRequirements"`
	BaseSalary         *ldSalary            `json:"baseSalary"`
}

// linkedInJobPage renders a JobPosting with the criteria, salary and
// applicant count the page's markup shows beside it.
func linkedInJobPage(
	doc *html.Node,
	address string,
	entities []ldEntity,
	renderer markdownRenderer,
) (string, bool, error) {
	var job ldJob
	if found, err := ldDecode(entities, "JobPosting", &job); !found {
		return "", false, err
	}
	title := string(job.Title)
	if title == "" {
		title = pageTitle(doc)
	}
	var locations []string
	for _, place := range job.JobLocation {
		if location := linkedInJoin(", ", string(place.Address.Locality), string(place.Address.Region),
			string(place.Address.Country)); location != "" {
			locations = append(locations, location)
		}
	}
	var employment []string
	for _, kind := range job.EmploymentType {
		if kind != "" {
			employment = append(employment, linkedInEmployment(string(kind)))
		}
	}
	salary := ""
	if node := firstClass(doc, "compensation__salary"); node != nil {
		salary = nodeText(node)
	}
	if salary == "" {
		salary = linkedInSalary(job.BaseSalary, address)
	}
	applicants := ""
	if node := firstClass(doc, "num-applicants__caption"); node != nil {
		applicants = nodeText(node)
	}

	var out strings.Builder
	out.WriteString("# " + title + "\n\n")
	for _, line := range [][]string{
		{"Company", string(job.HiringOrganization.Name), "Location", strings.Join(locations, " / ")},
		{
			"Posted", linkedInDay(string(job.DatePosted)), "Valid through", linkedInDay(string(job.ValidThrough)),
			"Employment type", strings.Join(employment, ", "),
		},
		{"Salary", salary, "Applicants", applicants},
	} {
		if meta := linkedInMeta(line...); meta != "" {
			out.WriteString(meta + "  \n")
		}
	}
	out.WriteString("**Job:** " + address + "\n\n")
	criteria := linkedInCriteria(doc)
	if len(criteria) == 0 {
		if industry := linkedInJoin(", ", linkedInTexts(job.Industry)...); industry != "" {
			criteria = append(criteria, "- **Industries:** "+industry)
		}
	}
	var education []string
	for _, credential := range job.Education {
		if credential != "" {
			education = append(education, string(credential))
		}
	}
	if len(education) > 0 {
		criteria = append(criteria, "- **Education:** "+strings.Join(education, ", "))
	}
	if skills := linkedInJoin(", ", linkedInTexts(job.Skills)...); skills != "" {
		criteria = append(criteria, "- **Skills:** "+skills)
	}
	if len(criteria) > 0 {
		out.WriteString("## Criteria\n\n" + strings.Join(criteria, "\n") + "\n\n")
	}
	out.WriteString("## Description\n\n")
	blocks, err := linkedInDescription(string(job.Description), renderer)
	if len(blocks) == 0 {
		if markup := firstClass(doc, "show-more-less-html__markup"); markup != nil {
			blocks = renderer.blocks(markup)
		}
	}
	switch {
	case len(blocks) > 0:
		out.WriteString(strings.Join(blocks, "\n\n") + "\n")
	case err != nil:
		out.WriteString("*The description could not be read: " + err.Error() + "*\n")
	default:
		out.WriteString("*The page states no description.*\n")
	}
	return out.String(), true, err
}

// linkedInDescription renders a job description's HTML as Markdown blocks;
// a run of hard breaks parts paragraphs.
func linkedInDescription(description string, renderer markdownRenderer) ([]string, error) {
	if strings.TrimSpace(description) == "" {
		return nil, nil
	}
	container := &html.Node{Type: html.ElementNode, Data: divTag, DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(description), container)
	if err != nil {
		return nil, fmt.Errorf("parse the job description's HTML: %w", err)
	}
	for _, node := range nodes {
		container.AppendChild(node)
	}
	blocks := renderer.blocks(container)
	for index, block := range blocks {
		blocks[index] = strings.TrimSpace(linkedInBreakRuns.ReplaceAllString(block, "\n\n"))
	}
	return blocks, nil
}

// linkedInEmployment renders a schema.org employment type ("FULL_TIME") as
// the page words it ("Full-time").
func linkedInEmployment(value string) string {
	words := strings.ToLower(strings.ReplaceAll(value, "_", "-"))
	if words == "" {
		return ""
	}
	return strings.ToUpper(words[:1]) + words[1:]
}

// linkedInSalary renders the baseSalary of the job at target; "" when it
// states none. A range stating one bound reads "from" its minimum or "up to"
// its maximum, never as an exact amount.
func linkedInSalary(salary *ldSalary, target string) string {
	if salary == nil || len(salary.Value) == 0 {
		return ""
	}
	var exact ldText
	if err := json.Unmarshal(salary.Value, &exact); err == nil && exact != "" {
		return linkedInJoin(" ", string(salary.Currency), string(exact))
	}
	var quantity struct {
		Value    ldText `json:"value"`
		MinValue ldText `json:"minValue"`
		MaxValue ldText `json:"maxValue"`
		UnitText ldText `json:"unitText"`
	}
	if err := json.Unmarshal(salary.Value, &quantity); err != nil {
		obs.Logger(context.Background()).Warn("harvest: a LinkedIn job's salary could not be read",
			"target", logSource(target), obs.FieldErr, err.Error())
		return ""
	}
	bound, amount := "", string(quantity.Value)
	switch {
	case quantity.MinValue != "" && quantity.MaxValue != "":
		amount = string(quantity.MinValue) + " – " + string(quantity.MaxValue)
	case quantity.MinValue != "":
		bound, amount = "from ", string(quantity.MinValue)
	case quantity.MaxValue != "":
		bound, amount = "up to ", string(quantity.MaxValue)
	}
	if amount == "" {
		return ""
	}
	text := bound + linkedInJoin(" ", string(salary.Currency), amount)
	if quantity.UnitText != "" {
		text += " per " + strings.ToLower(string(quantity.UnitText))
	}
	return text
}
