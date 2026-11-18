# Product Requirements Document (PRD)

# Project 3: Group Launchpad

**Product type:** Multi-platform automation system\
**Difficulty:** Intermediate\
**Version:** 1.0\
**Status:** Draft --- Ready for Technical Review\
**Integrations:** Slack API, Google Classroom API, Google Drive API\
**Core concepts:** OAuth 2.0, REST APIs, scheduled jobs, idempotency,
automation

------------------------------------------------------------------------

## 1. Executive Summary

### 1.1 Product Overview

Group Launchpad is a centralized automation platform that simplifies and
standardizes the process of launching student groups across multiple
platforms.

Today, coordinators manually create Slack channels, invite staff and
lecturers, create Google Classroom courses and Google Drive folders,
distribute student intake forms, monitor onboarding, and schedule
announcements. This repetitive process takes time and can lead to missed
steps.

Group Launchpad brings these operations into one application. A
coordinator enters group information once and launches the workflow. The
system performs the required operations across connected platforms,
tracks their completion, and monitors whether the group remains
correctly configured.

### 1.2 Problem Statement

The current process involves: - Repetitive manual work across multiple
platforms. - Inconsistent configurations between groups. - Human errors
and forgotten setup tasks. - No centralized view of onboarding
progress. - Difficulty identifying students who have not joined Slack. -
Manual communication and announcement scheduling. - No automated way to
detect configuration changes.

### 1.3 Proposed Solution

The system will: 1. Collect group information through a launch wizard.
2. Create required Slack channels. 3. Add predefined staff, lecturers,
and teaching assistants. 4. Create a Google Classroom course. 5. Create
a Google Drive folder structure. 6. Generate and distribute a student
intake form. 7. Track student onboarding. 8. Perform daily
synchronization audits. 9. Schedule announcements. 10. Notify
coordinators about important events and failures.

The system must support safe re-execution without creating duplicate
resources.

------------------------------------------------------------------------

## 2. Product Goals and Objectives

### 2.1 Primary Goals

-   Reduce the time required to launch a student group.
-   Eliminate repetitive manual setup.
-   Maintain consistency across Slack, Classroom, and Drive.
-   Provide visibility into student onboarding.
-   Detect missing or inconsistent resources.
-   Reduce operational mistakes.
-   Make group management scalable.

### 2.2 Success Metrics

  Metric                          Proposed target
  ------------------------------- ------------------------
  Manual group setup time         Reduce by at least 80%
  Resource creation               Automated
  Duplicate resources on re-run   Zero
  Daily synchronization           Automated
  Onboarding visibility           Available centrally
  Failed operations               Logged and visible
  Announcement scheduling         Automated

*These are proposed targets, not measured results.*

### 2.3 Non-Goals

The initial version will not: - Replace Slack, Google Classroom, or
Google Drive. - Implement a complete Learning Management System. -
Provide video conferencing. - Implement payments. - Replace the
organization's existing student database. - Build a full marketing
automation platform.

------------------------------------------------------------------------

## 3. Target Users and Roles

### 3.1 Coordinator

The primary user.

Responsibilities: - Create and configure groups. - Launch group setup. -
Monitor setup progress. - Review onboarding status. - Manage
announcements. - Resolve integration errors.

### 3.2 Administrator

Responsibilities: - Configure integrations. - Manage predefined staff. -
Manage application settings and permissions. - Monitor system health and
credentials.

### 3.3 Lecturer

Responsibilities: - Participate in the assigned group. - Access relevant
Slack channels, Classroom, and Drive resources.

### 3.4 Teaching Assistant (TA)

Responsibilities: - Assist lecturers. - Participate in Slack
communication. - Access assigned educational resources.

### 3.5 Student

Responsibilities: - Receive and submit the onboarding form. - Join the
appropriate Slack workspace and channels. - Access Classroom and Drive
resources.

------------------------------------------------------------------------

## 4. Functional Requirements

### 4.1 Group Creation Wizard

**Priority: P0 --- Critical**

The application must provide a centralized interface for creating a
group.

  Field                     Type            Required
  ------------------------- --------------- ----------
  Group name                String          Yes
  Course name               String          Yes
  Start date                Date            Yes
  Teaching staff            Multi-select    Yes
  Lecturers                 Multi-select    Yes
  Teaching assistants       Multi-select    No
  Student email addresses   List            Yes
  Group configuration       Configuration   Yes

**Expected behavior** 1. Coordinator opens the application and selects
"New Group". 2. The system displays the creation wizard. 3. Coordinator
enters the required information. 4. The system validates the
information. 5. Coordinator reviews and confirms the configuration. 6.
Coordinator selects "Launch Group". 7. The system creates an internal
group record and starts the automation workflow. 8. The coordinator can
see execution progress.

**Acceptance criteria** - Required fields cannot be empty. - Invalid
email addresses are rejected. - Start date must be valid. - Duplicate
group identifiers are prevented. - A group cannot be launched with
invalid configuration. - Submitted information is preserved.

### 4.2 Slack Integration

**Priority: P0 --- Critical**

The application must integrate with Slack through the Slack API.

**Requirements** 1. Connect to Slack using OAuth 2.0. 2. Create
predefined channels for each group. 3. Invite the eight predefined staff
members. 4. Invite assigned lecturers and teaching assistants. 5.
Support adding students to appropriate channels. 6. Publish welcome
messages. 7. Track student Slack onboarding status.

**Proposed default channels** - `#general` - `#announcements` -
`#questions` - `#resources`

Channel names must follow a consistent naming convention, for example:
`python-backend-13-general`.

The application must maintain a predefined list of eight staff members.
Each group can also have its own lecturers and TAs.

**Acceptance criteria** - Required channels are created. - Existing
channels are not duplicated. - Staff, lecturers, and TAs are invited. -
Failed invitations are recorded. - Student membership status can be
retrieved. - Welcome messages can be published or scheduled.

### 4.3 Google Classroom Integration

**Priority: P0 --- Critical**

The system must automatically create Google Classroom courses.

**Requirements** - Authenticate using Google OAuth 2.0. - Create a
Classroom course. - Assign the appropriate lecturer or course owner. -
Invite or enroll eligible students. - Store the Classroom course
identifier. - Link the external course to the internal group.

**Course information** - Course name and section. - Course
description. - Owner. - Associated group. - Start date.

**Acceptance criteria** - Course is created successfully. - Course ID is
stored. - Re-running the workflow does not create another course. -
Failures are logged and visible to the coordinator.

### 4.4 Google Drive Integration

**Priority: P0 --- Critical**

The application must create a dedicated Drive folder for each group.

**Proposed folder structure**

``` text
Group Launchpad/
└── Python Backend 13/
    ├── General/
    ├── Lectures/
    ├── Assignments/
    ├── Resources/
    └── Student Data/
```

**Requirements** - Create the main group folder and predefined
subfolders. - Store folder IDs and associate them with the group. -
Configure appropriate access permissions. - Display creation status. -
Prevent duplicate folders.

**Acceptance criteria** - Folder structure is created and IDs are
stored. - Coordinator can access the folder. - Restricted data is not
accessible to unauthorized users. - Re-running setup does not duplicate
folders.

### 4.5 Student Intake Form Generator

**Priority: P0 --- Critical**

The system must generate a group-specific student intake form.

**Proposed fields**

  Field                      Type
  -------------------------- --------
  Full name                  Text
  Email address              Email
  Phone number               Text
  Preferred contact method   Select
  Additional information     Text

The exact personal-data fields must be confirmed with the organization.

**Requirements** - Generate a form for each group. - Associate the form
with the internal group. - Distribute it to provided student email
addresses. - Collect responses and associate them with the correct
group. - Store response status. - Prevent duplicate student records.

**Acceptance criteria** - Each group has its own form. - Responses are
associated with the correct group. - Invalid submissions are rejected. -
Duplicate submissions are handled safely. - Distribution failures are
recorded.

### 4.6 Student Onboarding and Join Audit

**Priority: P0 --- Critical**

The system must compare the expected student roster with actual Slack
membership.

**Workflow** 1. Retrieve the expected student list. 2. Retrieve current
Slack group membership. 3. Compare both datasets. 4. Identify students
who have joined and students who have not. 5. Calculate onboarding
statistics. 6. Display results to the coordinator.

**Example**

``` text
Expected students: 24
Joined Slack:      21
Not joined:         3
Onboarding progress: 87.5%
```

**Requirements** - Track expected students and actual Slack members. -
Match students using email addresses or verified identifiers. - Detect
missing students. - Update onboarding status and display progress. -
Record audit timestamps.

**Acceptance criteria** - Joined and missing students are identified. -
Progress percentages are calculated correctly. - Audit results are
stored. - Coordinator can inspect missing students.

### 4.7 Daily Synchronization Audit

**Priority: P0 --- Critical**

The system must run an automated daily audit to ensure each group
remains correctly configured.

  Resource          Expected check
  ----------------- --------------------------------
  Slack channels    Required channels exist
  Slack staff       Required staff are members
  Slack lecturers   Assigned lecturers are members
  Slack students    Expected students are tracked
  Classroom         Course exists
  Drive             Required folders exist
  Intake form       Form exists and is accessible

**Audit workflow**

``` text
Scheduled Job
     |
     v
Load Active Groups
     |
     v
Retrieve Expected Configuration
     |
     v
Fetch External Platform Data
     |
     v
Compare Expected vs Actual
     |
     v
Identify Differences
     |
     v
Store Audit Results
     |
     v
Notify Coordinator
```

**Requirements** - Run once every 24 hours. - Support multiple groups. -
Detect missing resources and members. - Store audit history. - Generate
actionable reports. - Notify coordinators about inconsistencies.

**Important:** The initial implementation should detect and report
differences. Automatic repair should be a separate, controlled operation
to avoid unintended changes to external resources.

### 4.8 Announcement Scheduler

**Priority: P1 --- High**

The application must support scheduled communication with students and
staff.

**Announcement types** - Welcome announcement. - Staff introduction. -
Group rules. - Course start reminder. - General group notifications.

**Requirements** - Create announcement templates. - Associate
announcements with a group. - Configure scheduled delivery dates. -
Publish messages to appropriate Slack channels. - Track scheduled and
published announcements. - Record failed deliveries. - Prevent duplicate
publication.

**Example schedule**

  Announcement          Schedule
  --------------------- -------------------
  Welcome message       Group start date
  Staff introductions   Group start date
  Group rules           Group start date
  Course reminder       Configurable date

**Acceptance criteria** - Announcements can be scheduled and published
at the configured time. - Failed messages are logged. - Duplicate
announcements are prevented. - Coordinators can review announcement
status.

### 4.9 Coordinator Notifications

**Priority: P1 --- High**

The system must notify coordinators about: - Group setup completed or
partially failed. - Slack invitation failure. - Classroom or Drive
creation failure. - Incomplete student onboarding. - Audit
inconsistencies. - Announcement delivery failure.

Notifications may initially be delivered through Slack. Additional
channels can be introduced later.

------------------------------------------------------------------------

## 5. Core User Workflow

``` text
Coordinator
    |
    v
Create New Group
    |
    v
Enter Group Information
    |
    v
Validate Configuration
    |
    v
Launch Group
    |
    +----------------------+
    |          |           |
    v          v           v
  Slack    Classroom     Drive
    |          |           |
    +----------+-----------+
               |
               v
       Generate Intake Form
               |
               v
       Distribute Student Form
               |
               v
       Track Student Onboarding
               |
               v
       Schedule Announcements
               |
               v
       Daily Synchronization Audit
               |
               v
       Coordinator Dashboard
```

------------------------------------------------------------------------

## 6. User Interface Requirements

### 6.1 Main Dashboard

The dashboard must display: - Total and active groups. - Group launch
status. - Student onboarding progress. - Integration health. - Recent
audit results. - Failed operations. - Upcoming announcements.

### 6.2 New Group Screen

The screen must contain: - Group name and course. - Start date. -
Teaching staff selection. - Lecturer selection. - Student list. - Launch
button.

### 6.3 Launch Checklist

The system must display the status of each operation.

**Example**

``` text
Group Launchpad — Python Backend 13

[✓] Slack channels created (4)
[✓] Staff and lecturers added (10)
[✓] Classroom course created
[✓] Drive folder and roster view ready
[✓] Intake form sent to 24 students
[●] Joined Slack: 21 of 24
[○] Welcome posts scheduled
```

Possible statuses: Pending, In Progress, Completed, Failed, Requires
Attention.

### 6.4 Group Details Screen

The coordinator must be able to inspect group information, students,
staff, Slack resources, Classroom resources, Drive resources, onboarding
progress, audit history, and scheduled announcements.

------------------------------------------------------------------------

## 7. Technical and System Requirements

This section defines proposed technical requirements. The exact
implementation stack is subject to technical review.

### 7.1 Architecture

The application should use a modular architecture.

Main components: 1. Frontend Application 2. Backend API 3.
Authentication Module 4. Group Management Module 5. Slack Integration
Module 6. Google Classroom Integration Module 7. Google Drive
Integration Module 8. Intake Form Module 9. Audit Engine 10. Job
Scheduler 11. Notification Module 12. Database

**Proposed architecture**

``` text
             Frontend
                |
                v
           Backend API
                |
       +--------+--------+
       |        |        |
       v        v        v
     Group   Auth     Integration
   Management Module    Layer
                         |
                 +-------+-------+
                 |       |       |
                 v       v       v
               Slack  Classroom Drive
                 |
                 v
           Audit Engine
                 |
                 v
           Job Scheduler
                 |
                 v
              Database
                 |
                 v
           Notifications
```

### 7.2 Database

A relational database is recommended. PostgreSQL is a proposed choice.

**Initial data entities**

**Users** - id - full_name - email - role - created_at

**Groups** - id - name - course_name - start_date - status - created_at

**GroupMembers** - id - group_id - user_id - member_role -
onboarding_status

**Integrations** - id - provider - account_identifier -
connection_status - created_at

**GroupResources** - id - group_id - provider - resource_type -
external_resource_id - resource_name - status

**IntakeForms** - id - group_id - external_form_id - form_url - status

**AuditLogs** - id - group_id - audit_type - expected_state -
actual_state - differences - created_at

**ScheduledAnnouncements** - id - group_id - message_type -
message_content - scheduled_at - status - published_at

**AutomationJobs** - id - group_id - job_type - status - attempts -
error_message - started_at - completed_at

The final schema should be refined during database design.

### 7.3 API Requirements

The backend must expose RESTful endpoints.

  Method   Endpoint                          Purpose
  -------- --------------------------------- ------------------------
  POST     `/api/groups`                     Create group
  GET      `/api/groups`                     List groups
  GET      `/api/groups/:id`                 Get group details
  POST     `/api/groups/:id/launch`          Launch group
  GET      `/api/groups/:id/status`          Get launch status
  GET      `/api/groups/:id/members`         Get group members
  GET      `/api/groups/:id/audits`          Get audit history
  POST     `/api/groups/:id/audits`          Trigger manual audit
  GET      `/api/groups/:id/announcements`   List announcements
  POST     `/api/groups/:id/announcements`   Schedule announcement
  GET      `/api/integrations`               Get integration status

These are proposed API contracts and may change during implementation.

### 7.4 Authentication and Authorization

The system must support: - Secure coordinator authentication. -
Administrator permissions. - Role-based access control. - OAuth 2.0
integration with external services. - Secure token storage and token
refresh. - Access validation before external operations.

Google and Slack credentials must never be exposed to the frontend.

------------------------------------------------------------------------

## 8. Idempotency and Reliability

**Priority: P0 --- Critical**

One of the most important technical requirements is safe re-execution.

The same group launch operation may execute more than once because of
network failures, server restarts, API timeouts, user double-clicks, or
job retries. The system must not create duplicate resources.

**Example --- first execution**

``` text
Create Slack channel
Create Classroom course
Create Drive folder
```

**Example --- second execution**

``` text
Slack channel already exists -> Skip
Classroom course already exists -> Reuse
Drive folder already exists -> Reuse
```

**Requirements** 1. Store external resource identifiers. 2. Use stable
internal group identifiers. 3. Check whether a resource already exists
before creating it. 4. Use idempotency keys for launch requests and
supported operations. 5. Make background jobs safe to retry. 6. Track
execution state. 7. Log every external API operation. 8. Handle partial
failures.

**Partial failure example**

If Slack creation succeeds but Classroom creation fails, the system
must: - Preserve successful Slack resources. - Record the Classroom
failure. - Mark the launch as partially completed. - Allow the failed
operation to be retried. - Avoid recreating successful resources.

The system must not assume all external operations can be completed in
one database transaction.

------------------------------------------------------------------------

## 9. Non-Functional Requirements

### 9.1 Performance

-   Dashboard should load within 3 seconds under normal conditions.
-   Group creation requests should receive a prompt response.
-   Long-running integrations must execute asynchronously.
-   Background jobs must not block normal API requests.

### 9.2 Security

-   All communication must use HTTPS.
-   OAuth tokens must be encrypted at rest.
-   Secrets must be stored in environment variables or a secure secrets
    manager.
-   User permissions must be validated.
-   Sensitive student data must have restricted access.
-   Logs must not expose access tokens or unnecessary personal data.

### 9.3 Reliability

-   Failed operations must be recoverable.
-   Background jobs must support retries.
-   Duplicate execution must be prevented.
-   Every operation must have a traceable status.
-   Integration outages must not destroy existing group data.

### 9.4 Maintainability

-   Use modular architecture.
-   Separate integration logic from business logic.
-   Follow consistent coding standards.
-   Provide structured logging.
-   Write automated tests.
-   Document API contracts.

------------------------------------------------------------------------

## 10. Error Handling

  Scenario                         Expected behavior
  -------------------------------- ---------------------------------
  Slack API unavailable            Log error and retry
  Google OAuth expired             Attempt token refresh
  Classroom creation fails         Mark operation failed
  Drive folder already exists      Reuse existing resource
  Invalid student email            Reject or flag record
  Student does not join Slack      Mark as pending
  Scheduler execution fails        Log and retry
  Duplicate launch request         Return existing execution
  Announcement publication fails   Record failure and retry safely

The coordinator must be able to identify the reason for a failure
without accessing server logs.

------------------------------------------------------------------------

## 11. Testing Requirements

### 11.1 Unit Testing

Test group validation, student validation, idempotency logic, onboarding
calculations, audit comparison logic, and announcement scheduling logic.

### 11.2 Integration Testing

Test interactions with Slack API, Google Classroom API, Google Drive
API, the form service, and the database.

### 11.3 End-to-End Testing

Test the complete workflow: 1. Create a group. 2. Enter group
information. 3. Launch the group. 4. Verify Slack resources. 5. Verify
Classroom course. 6. Verify Drive folders. 7. Verify intake form. 8.
Verify student onboarding. 9. Run an audit. 10. Verify scheduled
announcements.

### 11.4 Idempotency Testing

Run the same launch request multiple times. Expected result: - No
duplicate channels, Classroom courses, or Drive folders. - No duplicate
invitations or announcements where the operation is not safely
repeatable. - Existing resources are reused.

### 11.5 Failure Testing

Simulate API timeouts, expired OAuth tokens, permission errors, database
failures, partial launch completion, and background worker restarts.

------------------------------------------------------------------------

## 12. Deployment Requirements

The application must be deployable to a production-like environment.

**Required components** - Frontend hosting. - Backend hosting. -
PostgreSQL database. - Background worker. - Job scheduler. - Environment
configuration. - OAuth credentials. - Logging and monitoring.

**Deployment checklist** - \[ \] Application builds successfully. - \[
\] Database migrations run successfully. - \[ \] Environment variables
are configured. - \[ \] OAuth integrations are configured. - \[ \] API
endpoints are accessible. - \[ \] Background worker is running. - \[ \]
Scheduled jobs are running. - \[ \] HTTPS is enabled. - \[ \] Logs are
accessible. - \[ \] Health checks are implemented. - \[ \] Production
smoke tests pass.

------------------------------------------------------------------------

## 13. Project Phases and Deliverables

### Phase 1 --- Foundation

Deliverables: project structure, database setup, backend API,
authentication, group creation, and basic dashboard.

**Definition of Done:** A coordinator can create and retrieve a group.

### Phase 2 --- External Integrations

Deliverables: Slack OAuth, Slack channel creation, staff invitations,
Google Classroom integration, and Google Drive integration.

**Definition of Done:** The system can create and associate external
resources with an internal group.

### Phase 3 --- Student Onboarding

Deliverables: intake form generation, student email distribution, form
response processing, Slack membership tracking, and onboarding
dashboard.

**Definition of Done:** The coordinator can monitor which students have
completed onboarding.

### Phase 4 --- Automation Engine

Deliverables: background job processing, idempotent launch workflow,
retry mechanism, daily synchronization audit, and audit history.

**Definition of Done:** The system can safely execute and re-execute
automation workflows.

### Phase 5 --- Announcements and Notifications

Deliverables: announcement templates, scheduled announcements, automated
publishing, coordinator notifications, and failure reporting.

**Definition of Done:** Announcements can be scheduled and delivered
automatically.

### Phase 6 --- Testing and Deployment

Deliverables: unit, integration, and end-to-end tests; production
configuration; deployment; and documentation.

**Definition of Done:** The application is deployed and its primary
workflow passes end-to-end testing.

------------------------------------------------------------------------

## 14. MVP Scope

  Feature                                   MVP
  ----------------------------------------- -------
  Group creation wizard                     Yes
  Slack channel creation                    Yes
  Staff invitations                         Yes
  Classroom course creation                 Yes
  Drive folder creation                     Yes
  Intake form generation                    Yes
  Student onboarding tracking               Yes
  Daily audit                               Yes
  Scheduled announcements                   Basic
  Coordinator notifications                 Basic
  Advanced analytics                        No
  Automatic repair of all inconsistencies   No

The MVP must demonstrate one complete group-launch workflow.

------------------------------------------------------------------------

## 15. Assumptions and Open Questions

The following decisions must be confirmed before implementation:

1.  Which frontend and backend technologies should be used?
2.  Will the application use an existing Slack workspace?
3.  Is there already a predefined list of eight staff members?
4.  Will lecturers be selected from a predefined database?
5.  Should students be invited to Slack automatically or through
    invitation links?
6.  Should Google Classroom courses be created under an existing Google
    Workspace organization?
7.  Should intake forms use Google Forms?
8.  Where should student intake responses be stored?
9.  Which Slack channels should be created by default?
10. Should daily audits only report inconsistencies or also repair them?
11. At what exact time should daily audits run?
12. Who is allowed to launch or modify groups?
13. Should announcements be editable after scheduling?
14. What notification channel should coordinators use?

These questions should be resolved before finalizing the technical
architecture.

------------------------------------------------------------------------

## 16. Final Product Acceptance Criteria

Group Launchpad will be considered successfully implemented when: - A
coordinator can create a student group using one form. - The system can
create required Slack channels. - The system can invite staff and
lecturers. - The system can create a Google Classroom course. - The
system can create a Google Drive folder structure. - The system can
generate and distribute an intake form. - Student onboarding status can
be monitored. - The system can perform daily synchronization audits. -
Announcements can be scheduled and published. - Integration failures are
visible and recoverable. - Re-running operations does not create
duplicate resources. - The application is tested and deployed
successfully.

------------------------------------------------------------------------

## 17. Project Definition of Success

The ultimate objective of Group Launchpad is to transform a repetitive,
multi-platform manual process into a reliable, centralized automation
workflow.

A coordinator should be able to create a group, launch all required
resources, monitor student onboarding, and maintain synchronization
across platforms without manually repeating the same setup checklist.

**One group. One launch action. Every platform synchronized.**
