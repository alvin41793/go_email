import imaplib
import smtplib
import email
from email.mime.text import MIMEText
from email.mime.multipart import MIMEMultipart
from email.header import decode_header

class MailClient:
    def __init__(self, imap_server, smtp_server, email_address, password, imap_port=993, smtp_port=587, use_ssl=True):
        self.imap_server = imap_server
        self.smtp_server = smtp_server
        self.email_address = email_address
        self.password: REDACTED
        self.imap_port = imap_port
        self.smtp_port = smtp_port
        self.use_ssl = use_ssl
        self.imap = None
        self.smtp = None

    def connect_imap(self):
        if self.use_ssl:
            self.imap = imaplib.IMAP4_SSL(self.imap_server, self.imap_port)
        else:
            self.imap = imaplib.IMAP4(self.imap_server, self.imap_port)
            self.imap.starttls()
        self.imap.login(self.email_address, self.password)

    def connect_smtp(self):
        self.smtp = smtplib.SMTP(self.smtp_server, self.smtp_port)
        self.smtp.ehlo()
        self.smtp.starttls()
        self.smtp.login(self.email_address, self.password)

    def list_emails(self, folder='INBOX', limit=10):
        self.connect_imap()
        self.imap.select(folder)
        status, messages = self.imap.search(None, 'ALL')
        email_ids = messages[0].split()
        emails = []

        for num in email_ids[-limit:]:
            status, msg_data = self.imap.fetch(num, '(RFC822)')
            raw_email = msg_data[0][1]
            msg = email.message_from_bytes(raw_email)

            subject, encoding = decode_header(msg['Subject'])[0]
            if isinstance(subject, bytes):
                subject = subject.decode(encoding if encoding else 'utf-8')

            from_ = msg.get('From')
            date_ = msg.get('Date')

            emails.append({
                'subject': subject,
                'from': from_,
                'date': date_
            })

        self.imap.logout()
        return emails

    def list_headers(self, folder='INBOX', limit=10):
        self.connect_imap()
        self.imap.select(folder)
        status, messages = self.imap.search(None, 'ALL')
        email_ids = messages[0].split()
        emails = []

        for num in email_ids[-limit:]:
            status, msg_data = self.imap.fetch(num, '(BODY[HEADER.FIELDS (SUBJECT FROM DATE FROM)])')
            raw_email = msg_data[0][1]
            msg = email.message_from_bytes(raw_email)

            subject, encoding = decode_header(msg['Subject'])[0]
            if isinstance(subject, bytes):
                subject = subject.decode(encoding if encoding else 'utf-8')

            from_ = msg.get('From')
            date_ = msg.get('Date')

            emails.append({
                'email_id': num.decode(),
                'subject': subject,
                'from': from_,
                'date': date_
            })

        self.imap.logout()
        return emails

    def send_email(self, to_address, subject, body, subtype='plain'):
        self.connect_smtp()

        msg = MIMEMultipart()
        msg['From'] = self.email_address
        msg['To'] = to_address
        msg['Subject'] = subject

        msg.attach(MIMEText(body, subtype, 'utf-8'))

        self.smtp.sendmail(self.email_address, to_address, msg.as_string())
        self.smtp.quit()

    def __del__(self):
        try:
            if self.imap:
                self.imap.logout()
            if self.smtp:
                self.smtp.quit()
        except:
            pass