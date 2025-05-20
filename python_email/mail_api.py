from MailClient import MailClient
from flask import Flask, request, jsonify, send_file, Response, stream_with_context
import imaplib
import smtplib
import email
import os
from email.header import decode_header
from io import BytesIO
from werkzeug.utils import secure_filename
from flask_cors import CORS
import json

app = Flask(__name__)
CORS(app)

# 邮箱客户端初始化（建议放配置文件/环境变量里）
mail_client = MailClient(
    imap_server='imap.ipage.com',
    smtp_server='smtp.ipage.com',
    email_address='aiteam@primeagencygroup.com',  # 替换成你的邮箱
    password: REDACTED             # 替换成你的密码
)

# 统一邮箱配置
EMAIL_ADDRESS = 'aiteam@primeagencygroup.com'
EMAIL_password: REDACTED
IMAP_SERVER = 'imap.ipage.com'
# 附件缓存（内存缓存）
attachments_cache = {}

@app.route('/sendmail', methods=['POST'])
def send_mail():
    data = request.json
    to_address = data.get('to')
    subject = data.get('subject')
    body = data.get('body')

    if not to_address or not subject or not body:
        return jsonify({'error': 'Missing required fields'}), 400

    try:
        mail_client.send_email(to_address, subject, body)
        return jsonify({'message': 'Email sent successfully'})
    except Exception as e:
        return jsonify({'error': str(e)}), 500

@app.route('/listmails', methods=['GET'])
def list_mails():
    limit = request.args.get('limit', default=5, type=int)

    def generate():
        try:
            mail = imaplib.IMAP4_SSL(IMAP_SERVER)
            mail.login(EMAIL_ADDRESS, EMAIL_PASSWORD)
            mail.select('inbox')

            yield '['
            status, messages = mail.search(None, 'ALL')
            email_ids = messages[0].split()[-10:]  # 限制数量

            first = True
            for eid in email_ids:
                if not first:
                    yield ','
                first = False
                try:
                    status, msg_data = mail.fetch(eid, '(BODY[HEADER.FIELDS (SUBJECT FROM DATE)])')
                    raw_email = msg_data[0][1]
                    if not raw_email:
                        continue
                    msg = email.message_from_bytes(raw_email)

                    subject, encoding = decode_header(msg['Subject'])[0]
                    if isinstance(subject, bytes):
                        subject = subject.decode(encoding if encoding else 'utf-8')

                    from_ = msg.get('From')
                    date_ = msg.get('Date')

                    email_info = {
                        'email_id': eid.decode(),
                        'subject': subject,
                        'from': from_,
                        'date': date_
                    }
                    yield json.dumps(email_info)
                except Exception as e:
                    continue
            yield ']'

            mail.logout()
        except Exception as e:
            yield json.dumps({'error': str(e)})

    return Response(stream_with_context(generate()), mimetype='application/json')

@app.route('/list_attachments', methods=['GET'])
def list_attachments():
    email_id = request.args.get('email_id')
    if not email_id:
        return jsonify({'error': 'Missing email_id'}), 400

    try:
        mail = imaplib.IMAP4_SSL(IMAP_SERVER)
        mail.login(EMAIL_ADDRESS, EMAIL_PASSWORD)
        mail.select('inbox')
        status, msg_data = mail.fetch(email_id, '(RFC822)')
        raw_email = msg_data[0][1]
        msg = email.message_from_bytes(raw_email)

        attachments = []

        for part in msg.walk():
            if part.get_content_maintype() == 'multipart':
                continue
            if part.get('Content-Disposition') is None:
                continue

            part_filename = part.get_filename()
            if part_filename:
                part_filename, encoding = decode_header(part_filename)[0]
                if isinstance(part_filename, bytes):
                    part_filename = part_filename.decode(encoding if encoding else 'utf-8')
                part_filename = secure_filename(part_filename)
                size = len(part.get_payload(decode=True)) if part.get_payload(decode=True) else 0
                mime_type = part.get_content_type()

                attachments.append({
                    'filename': part_filename,
                    'size_kb': round(size / 1024, 2),
                    'mime_type': mime_type
                })

        return jsonify(attachments)
    except Exception as e:
        return jsonify({'error': str(e)}), 500
    finally:
        try:
            mail.logout()
        except:
            pass

@app.route('/download_attachment', methods=['GET'])
def download_attachment():
    email_id = request.args.get('email_id')
    filename = request.args.get('filename')

    if not email_id or not filename:
        return jsonify({'error': 'Missing email_id or filename'}), 400

    cache_key = f"{email_id}_{filename}"
    if cache_key in attachments_cache:
        file_stream = BytesIO(attachments_cache[cache_key])
        return send_file(file_stream, as_attachment=True, download_name=filename)

    try:
        # 单独连接IMAP取附件
        mail = imaplib.IMAP4_SSL(IMAP_SERVER)
        mail.login(EMAIL_ADDRESS, EMAIL_PASSWORD)
        mail.select('inbox')
        
        # 确保email_id是字节类型
        if isinstance(email_id, str):
            email_id = email_id.encode('utf-8')
            
        status, msg_data = mail.fetch(email_id, '(RFC822)')
        
        if not msg_data or not msg_data[0]:
            return jsonify({'error': '无法获取邮件内容'}), 404
            
        raw_email = msg_data[0][1]
        msg = email.message_from_bytes(raw_email)

        for part in msg.walk():
            if part.get_content_maintype() == 'multipart':
                continue
            if part.get('Content-Disposition') is None:
                continue

            part_filename = part.get_filename()
            if part_filename:
                part_filename, encoding = decode_header(part_filename)[0]
                if isinstance(part_filename, bytes):
                    part_filename = part_filename.decode(encoding if encoding else 'utf-8')
                part_filename = secure_filename(part_filename)

                if part_filename == filename:
                    data = part.get_payload(decode=True)
                    if not data:
                        return jsonify({'error': '附件内容为空'}), 404
                        
                    attachments_cache[cache_key] = data
                    file_stream = BytesIO(data)
                    
                    # 添加调试信息
                    print(f"找到附件: {filename}, 大小: {len(data)} 字节")
                    
                    # 设置正确的MIME类型
                    mime_type = part.get_content_type() or 'application/octet-stream'
                    
                    return send_file(
                        file_stream, 
                        mimetype=mime_type,
                        as_attachment=True, 
                        download_name=filename
                    )

        return jsonify({'error': '未找到附件'}), 404
    except Exception as e:
        import traceback
        print(f"下载附件错误: {str(e)}")
        print(traceback.format_exc())
        return jsonify({'error': str(e)}), 500
    finally:
        try:
            mail.logout()
        except:
            pass

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=5001)