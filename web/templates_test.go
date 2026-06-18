package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadPageTemplate(t *testing.T) {
	template, err := loadPageTemplate("default")
	assert.NoError(t, err)
	assert.NotNil(t, template)
}

func TestLoadPageTemplateEmptySet(t *testing.T) {
	template, err := loadPageTemplate("")
	assert.NoError(t, err)
	assert.NotNil(t, template)
}

func TestLoadPageTemplateInvalidPath(t *testing.T) {
	_, err := loadPageTemplate("../invalid")
	assert.Error(t, err)
}

func TestIconNamePattern(t *testing.T) {
	validNames := []string{"fa-home", "lucide-star", "tabler-user"}
	for _, name := range validNames {
		assert.Regexp(t, iconNamePattern, name)
	}

	invalidNames := []string{"fa home", "lucide:star", "UPPERCASE"}
	for _, name := range invalidNames {
		assert.NotRegexp(t, iconNamePattern, name)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	result := firstNonEmpty("value1", "value2", "value3")
	assert.Equal(t, "value1", result)

	result = firstNonEmpty("", "value2", "value3")
	assert.Equal(t, "value2", result)

	result = firstNonEmpty("value1", "", "value3")
	assert.Equal(t, "value1", result)

	result = firstNonEmpty("", "", "")
	assert.Equal(t, "", result)

	result = firstNonEmpty("single")
	assert.Equal(t, "single", result)

	result = firstNonEmpty("")
	assert.Equal(t, "", result)
}

func TestDefaultTarget(t *testing.T) {
	result := defaultTarget("")
	assert.Equal(t, "_self", result)

	result = defaultTarget("_blank")
	assert.Equal(t, "_blank", result)
}

func TestEnsureLinkGroup(t *testing.T) {
	groups := make(map[string]DashboardLinkGroup)
	ensureLinkGroup(groups, "test-group")

	assert.Contains(t, groups, "test-group")
	assert.Equal(t, "test-group", groups["test-group"].Name)
}

func TestPriorityClassRank(t *testing.T) {
	assert.Equal(t, 0, priorityClassRank("first"))
	assert.Equal(t, 0, priorityClassRank("FIRST"))
	assert.Equal(t, 1, priorityClassRank("normal"))
	assert.Equal(t, 1, priorityClassRank(""))
	assert.Equal(t, 2, priorityClassRank("last"))
}
