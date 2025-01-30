package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	// credit to @reteps on github for the powerschool package
	"ps-diff/powerschool"
	//
	"strings"
	"time"
)

// ANSI escape sequences for colored logging:
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorCyan   = "\033[36m"
)

type Class struct {
	ID    int64
	Name  string
	Grade string
}

type Assignment struct {
	ID        int64
	Name      string
	Grade     string
	ClassID   int64
	ClassName string
}

type WebhookMessage struct {
	Content string `json:"content"`
}

const (
	powerschoolUrl      = "https://example.powerschool.com"
	powerschoolUsername = "<YOUR_POWERSCHOOL_PARENT_USERNAME>"
	powerschoolPassword = "<YOUR_POWERSCHOOL_PARENT_PASSWORD>"
	discordWebhookURL   = "<YOUR_DISCORD_WEBHOOK_URL>"

	backupClassesFile     = "backup_classes.json"
	backupAssignmentsFile = "backup_assignments.json"
)

// ----- Colored Logging Helpers -----
func logInfo(msg string) {
	fmt.Printf("%s[INFO] %s%s\n", ColorCyan, msg, ColorReset)
}

func logWarning(msg string) {
	fmt.Printf("%s[WARN] %s%s\n", ColorYellow, msg, ColorReset)
}

func logSuccess(msg string) {
	fmt.Printf("%s[SUCCESS] %s%s\n", ColorGreen, msg, ColorReset)
}

func logError(msg string) {
	fmt.Printf("%s[ERROR] %s%s\n", ColorRed, msg, ColorReset)
}

// ----- Backup/Restore Functions -----
func loadBackupDataClasses(filename string) ([]Class, error) {
	var classes []Class

	file, err := os.Open(filename)
	if err != nil {
		// If file doesn't exist, return empty slice
		return []Class{}, err
	}
	defer file.Close()

	bytesData, err := io.ReadAll(file)
	if err != nil {
		return []Class{}, err
	}

	err = json.Unmarshal(bytesData, &classes)
	if err != nil {
		return []Class{}, err
	}

	return classes, nil
}

func loadBackupDataAssignments(filename string) ([]Assignment, error) {
	var assignments []Assignment

	file, err := os.Open(filename)
	if err != nil {
		return []Assignment{}, err
	}
	defer file.Close()

	bytesData, err := io.ReadAll(file)
	if err != nil {
		return []Assignment{}, err
	}

	err = json.Unmarshal(bytesData, &assignments)
	if err != nil {
		return []Assignment{}, err
	}

	return assignments, nil
}

func saveBackupDataClasses(filename string, classes []Class) error {
	bytesData, err := json.MarshalIndent(classes, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filename, bytesData, 0644)
}

func saveBackupDataAssignments(filename string, assignments []Assignment) error {
	bytesData, err := json.MarshalIndent(assignments, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filename, bytesData, 0644)
}

// ----- Discord Notifications -----
func sendDiscordNotification(message string) {
	if message == "" {
		return
	}

	payload := WebhookMessage{Content: message}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		logError("Error marshaling JSON: " + err.Error())
		return
	}

	resp, err := http.Post(discordWebhookURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		logError("Error sending webhook: " + err.Error())
		return
	}
	defer resp.Body.Close()
	logSuccess("Discord notification sent!")
}

func compareAssignmentsAndNotifyChanges(oldAssignments, newAssignments []Assignment) {
	changes := []string{}
	oldAssignmentMap := make(map[int64]Assignment)

	for _, assignment := range oldAssignments {
		oldAssignmentMap[assignment.ID] = assignment
	}

	for _, newAssignment := range newAssignments {
		if oldAssignment, exists := oldAssignmentMap[newAssignment.ID]; exists {
			if oldAssignment.Grade != newAssignment.Grade {
				changes = append(changes, fmt.Sprintf(
					"Grade changed for assignment '%s' in class %s: %s -> %s",
					newAssignment.Name, newAssignment.ClassName, oldAssignment.Grade, newAssignment.Grade))
			}
			delete(oldAssignmentMap, newAssignment.ID)
		} else {
			changes = append(changes, fmt.Sprintf(
				"New assignment added: '%s' in class %s with grade %s",
				newAssignment.Name, newAssignment.ClassName, newAssignment.Grade))
		}
	}

	for _, deletedAssignment := range oldAssignmentMap {
		changes = append(changes, fmt.Sprintf(
			"Assignment removed: '%s' from class %s",
			deletedAssignment.Name, deletedAssignment.ClassName))
	}

	if len(changes) > 0 {
		sendDiscordNotification(strings.Join(changes, "\n"))
	} else {
		logInfo("No changes in Assignments.")
	}
}

func compareGradesAndNotifyChanges(oldClasses, newClasses []Class) {
	changes := []string{}
	oldGrades := make(map[int64]string)

	for _, class := range oldClasses {
		oldGrades[class.ID] = class.Grade
	}

	for _, class := range newClasses {
		if oldGrade, exists := oldGrades[class.ID]; exists {
			if oldGrade != class.Grade {
				changes = append(changes, fmt.Sprintf(
					"Grade changed for %s: %s -> %s",
					class.Name, oldGrade, class.Grade))
			}
		} else {
			changes = append(changes, fmt.Sprintf(
				"New class added: %s with grade %s",
				class.Name, class.Grade))
		}
	}

	if len(changes) > 0 {
		sendDiscordNotification(strings.Join(changes, "\n"))
	} else {
		logInfo("No changes in Classes.")
	}
}

// ----- The Main Logic -----
func fetchAndCompare() {
	logInfo("Starting data fetch and comparison...")

	// Load old data from backup
	oldClasses, err1 := loadBackupDataClasses(backupClassesFile)
	oldAssignments, err2 := loadBackupDataAssignments(backupAssignmentsFile)
	if err1 != nil {
		logWarning("Could not load old classes, possibly first run.")
	}
	if err2 != nil {
		logWarning("Could not load old assignments, possibly first run.")
	}

	// Fetch new data
	client := powerschool.Client(powerschoolUrl)
	student, err := client.GetStudent(powerschoolUsername, powerschoolPassword)
	if err != nil {
		logError("Failed to get student data: " + err.Error())
		return
	}

	// Build map for new data
	idMap := make(map[int64]string)
	for _, course := range student.Sections {
		idMap[course.Id] = course.SchoolCourseTitle
	}

	allowedTerms := make(map[int64]bool)
	termBeginDate, _ := time.Parse("2006-01-02", "2100-01-01")
	termDueDate, _ := time.Parse("2006-01-02", "2000-01-01")
	for _, reportingTerm := range student.ReportingTerms {
		if time.Now().After(reportingTerm.StartDate) &&
			time.Now().Before(reportingTerm.EndDate) &&
			strings.HasPrefix(reportingTerm.Title, "Q") {
			allowedTerms[reportingTerm.Id] = true
			if termDueDate.Before(reportingTerm.EndDate) {
				termDueDate = reportingTerm.EndDate
			}
			if termBeginDate.After(reportingTerm.StartDate) {
				termBeginDate = reportingTerm.StartDate
			}
		}
	}

	var newClasses []Class
	for _, finalGrade := range student.FinalGrades {
		if allowedTerms[finalGrade.ReportingTermId] {
			newClasses = append(newClasses, Class{
				ID:    finalGrade.Sectionid,
				Name:  idMap[finalGrade.Sectionid],
				Grade: finalGrade.Grade,
			})
		}
	}

	assignmentScoreMap := make(map[int64]string)
	for _, assignment := range student.AssignmentScores {
		if assignment.Score != "" {
			assignmentScoreMap[assignment.AssignmentId] = fmt.Sprintf("%s%%", assignment.Score)
		}
	}

	var newAssignments []Assignment
	for _, assignment := range student.Assignments {
		if assignment.DueDate.Before(termDueDate) && assignment.DueDate.After(termBeginDate) {
			if _, exists := assignmentScoreMap[assignment.Id]; !exists {
				continue
			}
			className := ""
			for _, class := range newClasses {
				if class.ID == assignment.Sectionid {
					className = class.Name
					break
				}
			}
			newAssignments = append(newAssignments, Assignment{
				ID:        assignment.Id,
				Name:      assignment.Name,
				Grade:     assignmentScoreMap[assignment.Id],
				ClassID:   assignment.Sectionid,
				ClassName: className,
			})
		}
	}

	// Compare new vs. old
	compareGradesAndNotifyChanges(oldClasses, newClasses)
	compareAssignmentsAndNotifyChanges(oldAssignments, newAssignments)

	// Save new data as old
	if err := saveBackupDataClasses(backupClassesFile, newClasses); err != nil {
		logError("Failed to backup new classes data: " + err.Error())
	}
	if err := saveBackupDataAssignments(backupAssignmentsFile, newAssignments); err != nil {
		logError("Failed to backup new assignments data: " + err.Error())
	}

	logInfo("Data fetch and comparison completed.")
}

func main() {
	// We'll run this check every 30 seconds
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Run it immediately once
	fetchAndCompare()

	// Then run continuously on each tick
	for range ticker.C {
		fetchAndCompare()
	}
}
