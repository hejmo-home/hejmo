package server_test

import (
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/store"
)

// mealOptionsForTest writes entries like a parent with an English browser.
func mealOptionsForTest() store.MealOptions {
	labels := map[meal.Mealtime]string{meal.Breakfast: "Breakfast", meal.Lunch: "Lunch", meal.Snack: "Snack", meal.Dinner: "Dinner"}
	return store.MealOptions{
		CalendarName: "Meal plan", Location: berlinTZ,
		Title: func(m meal.Mealtime, text string) string { return meal.EventTitle(labels[m], text) },
	}
}
