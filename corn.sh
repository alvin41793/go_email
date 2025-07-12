*/3 * * * * curl -X POST -H "token:eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjo2NjYsImV4cCI6MjEwODI1NzE2MywiaXNzIjoiZ2luLWJsb2cifQ.6gyUwAAjObhSlseb5E84MFS1HWmeD8LaYuh1lFkEVvQ" -H "Content-Type: application/json" -d '{"node": 2,"sync_limit":10}' http://localhost:7080/api/v1/emails/content

