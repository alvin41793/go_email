*/1 * * * * curl -X POST -H "token:eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjo2NjYsImV4cCI6MjEwODI1NzE2MywiaXNzIjoiZ2luLWJsb2cifQ.6gyUwAAjObhSlseb5E84MFS1HWmeD8LaYuh1lFkEVvQ" -H "Content-Type: application/json" -d '{"folder":"INBOX","limit":500}' http://localhost:7080/api/v1/emails/list



*/2 * * * * curl -X POST -H "token:eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjo2NjYsImV4cCI6MjEwODI1NzE2MywiaXNzIjoiZ2luLWJsb2cifQ.6gyUwAAjObhSlseb5E84MFS1HWmeD8LaYuh1lFkEVvQ" -H "Content-Type: application/json" -d '{"limit":10}' http://localhost:7080/api/v1/emails/content