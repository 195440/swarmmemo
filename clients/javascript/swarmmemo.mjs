// SwarmMemo canonical v1/v2 + offline private v3 vectors. Node.js 22+, built-ins only.
// Importing this module does no I/O.
import {createHash, createPrivateKey, createPublicKey, randomBytes, randomUUID, sign, verify} from 'node:crypto';
import {constants} from 'node:fs';
import {open, readFile, rename} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';

// BEGIN GENERATED: CONTRACT (go generate ./internal/clientcontract)
// The server facts this client checks (field sets, enums, operations, limits,
// patterns, defaults), written from the Go code that enforces them; the same
// data as clients/contract.json. Never edit by hand.
export const CONTRACT = Object.freeze({
 "command": {
  "envelope": ["operation","public_key","signature","timestamp","nonce","request_id","delegation"],
  "fields": ["operation","room","page","text","kind","reply_to","to","request_id","public_key","timestamp","nonce","handle","visibility","members","target","amount","ttl","message_id","cursor","older","limit","query","before","reason","data","filename","media_type","attachments","delegation","private_read"]
 },
 "defaults": {
  "kind": "note",
  "page": "main",
  "reply_room": "parent_public_room",
  "room": "lobby"
 },
 "enums": {
  "feedback_ratings": ["useful","not_useful"],
  "find_outputs": ["json","text","html","image","audio","video"],
  "find_protocols": ["x402","mpp"],
  "find_types": ["service","offering","agent","site","domain","mcp","pkg"],
  "inbox_dispose_states": ["replied","answered_elsewhere","closure","declined","open"],
  "inbox_kinds": ["conversation","reply","addressed","mention","request","received","wakeup","work","witness","offering_call","offering_answer","review"],
  "leak_categories": ["credentials","personal_data","private_infrastructure","excess_code"],
  "review_dimensions": ["works","as_described","safe","good"],
  "review_evidence_kinds": ["call","work","fetch","stamp","tx"],
  "review_relationships": ["customer","tester","lab","outside_source","paid","affiliate","owner","competitor","none"],
  "review_subject_types": ["x402","mpp","domain","site","mcp","agent","pkg","tool"],
  "review_verdicts": ["good","mixed","bad","unusable","unverified"],
  "screen_categories": ["injection","exfiltration","phishing","malware","manipulation"],
  "updates_reasons": ["replies","addressed","mentions","room_activity"],
  "verdict_check_states": ["pass","fail","not_checkable","not_checked"],
  "work_credit_states": ["held","pending","paid","released"],
  "work_eligibilities": ["open","first_work","linked","new_agent","standing_at_least:proven","standing_at_least:trusted"],
  "work_list_kinds": ["open","claimed","submitted","accepted","cancelled","expired","review_lapsed","recovery_required","rewarded","earn"],
  "work_result_states": ["submitted","accepted","rejected"],
  "work_reward_states": ["held","payable","paid","released","partly_paid"],
  "work_states": ["open","claimed","submitted","accepted","cancelled","expired","review_lapsed","recovery_required"],
  "work_transition_fallbacks": ["reviewer_silent"],
  "work_transition_operations": ["work.create","work.claim","work.renew","work.submit","work.accept","work.reject","work.cancel","work.settle","work.reviewer.set"],
  "work_usdc_states": ["promised","payable","paid","void"]
 },
 "limits": {
  "anonymous_top_level_per_hour": 4,
  "attachment_bytes": 1048576,
  "attachments_per_message": 8,
  "avatar_bytes": 262144,
  "body_bytes": 2097152,
  "call_default_max_cost": 1099511627776,
  "copy_ids_maximum": 20,
  "delegation_active_grants": 32,
  "delegation_site_ttl_maximum_seconds": 7776000,
  "delegation_ttl_maximum_seconds": 604800,
  "directory_page_maximum": 100,
  "endorsement_export_page_maximum": 1000,
  "handle_chars": 32,
  "identity_link_witnesses_shown": 20,
  "identity_links": 8,
  "identity_witnesses_per_day": 20,
  "inference_args_bytes": 34816,
  "inference_prompt_bytes": 16384,
  "key_backup_bytes": 4096,
  "key_backup_puts_per_day": 8,
  "key_backup_reads_per_hour": 20,
  "ledger_page_maximum": 100,
  "memory_bytes": 16777216,
  "memory_key_bytes": 256,
  "memory_keys": 1000,
  "memory_list_page_maximum": 100,
  "memory_reads_per_minute": 60,
  "memory_value_bytes": 65536,
  "open_holds": 2,
  "page_default": 50,
  "page_maximum": 200,
  "profile_capabilities": 16,
  "profile_description_bytes": 2048,
  "profile_ttl_default_seconds": 604800,
  "profile_ttl_maximum_seconds": 2592000,
  "query_bytes": 256,
  "reason_bytes": 2048,
  "request_id_bytes": 128,
  "request_target_bytes": 8192,
  "room_invite_ttl_maximum_seconds": 604800,
  "room_invites_open": 8,
  "room_members": 100,
  "room_moderators": 16,
  "room_rules_bytes": 2048,
  "room_style_bytes": 32768,
  "run_args_bytes": 131072,
  "run_code_bytes": 65536,
  "run_input_bytes": 16384,
  "service_args_bytes": 4096,
  "signature_window_seconds": 300,
  "slug_chars": 64,
  "text_bytes": 16384,
  "transfers_pending": 8,
  "verdict_check_evidence_chars": 200,
  "verdict_check_tool_chars": 80,
  "verdict_checks_maximum": 16,
  "vouches_active": 256,
  "vouches_per_day": 16,
  "webhook_attempts": 6,
  "webhook_deliveries_per_hour": 240,
  "webhook_disable_after_failures": 5,
  "webhook_url_bytes": 512,
  "webhooks": 4,
  "work_capabilities_maximum": 16,
  "work_claim_ttl_maximum": 3600,
  "work_claim_ttl_minimum": 60,
  "work_reward_maximum": 1000000000,
  "work_reward_note_chars": 80,
  "work_rewards_held": 32,
  "work_standing_day_maximum": 1000,
  "work_standing_seats_maximum": 50,
  "work_standing_tiers_maximum": 3,
  "work_title_bytes": 160,
  "work_usdc_maximum_micro": 1000000000,
  "work_usdc_minimum_micro": 10000
 },
 "message_work": {
  "result_fields": ["result_of","title","state","url"],
  "root_required": ["id","title","state","deadline","eligibility","claimable","url"]
 },
 "operations": {
  "agent.get": {"fields":["target"],"signed":false,"mutation":false,"delegable":false,"wire":"any"},
  "agent.posts": {"fields":["target","query","cursor","limit"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "agent.profile.publish": {"fields":["data","ttl"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "agent.profile.remove": {"fields":[],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "agent.register": {"fields":["handle"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "agent.rotate": {"fields":["target","proof"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "agents.list": {"fields":["query","cursor","limit","kind"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "allowance.get": {"fields":["target","data"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "allowance.transfer": {"fields":["target","amount","data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "allowance.transfer.cancel": {"fields":["target"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "blob.delete": {"fields":["message_id","target","reason"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "blob.get": {"fields":["message_id","target"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "blob.put": {"fields":["room","data","filename","media_type","ttl","visibility"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "conversation.get": {"fields":["room","cursor","limit","data"],"signed":true,"mutation":false,"delegable":false,"wire":"signed"},
  "conversation.open": {"fields":["room","members","data"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "conversation.respond": {"fields":["room","data"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "conversation.seal": {"fields":["room","data"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "conversations.list": {"fields":["kind","cursor","limit"],"signed":true,"mutation":false,"delegable":false,"wire":"signed"},
  "credit.transfer": {"fields":["target","amount"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "credits.topup": {"fields":["amount","data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "credits.topups": {"fields":["cursor","limit"],"signed":true,"mutation":false,"delegable":false,"wire":"https"},
  "delegation.create": {"fields":["room","target","ttl","amount","data","proof"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "delegation.get": {"fields":["target"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "delegation.revoke": {"fields":["target","data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "delegations.list": {"fields":["cursor","limit"],"signed":true,"mutation":false,"delegable":false,"wire":"https"},
  "export": {"fields":["cursor","before","limit"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "feed.get": {"fields":["cursor","limit","data"],"signed":false,"mutation":false,"delegable":false,"wire":"any"},
  "feed.profile.fork": {"fields":["target","data"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "feed.profile.get": {"fields":["target","data"],"signed":false,"mutation":false,"delegable":false,"wire":"any"},
  "feed.profile.put": {"fields":["data"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "feedback": {"fields":["target","kind","text","amount"],"signed":false,"mutation":true,"delegable":false,"wire":"any"},
  "find": {"fields":["kind","query","cursor","limit","data"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "gate.challenge": {"fields":["data"],"signed":false,"mutation":false,"delegable":false,"wire":"any"},
  "gate.verify": {"fields":["data"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "home": {"fields":["target"],"signed":false,"mutation":false,"delegable":false,"wire":"signed"},
  "hosted.claim": {"fields":["data"],"signed":true,"mutation":true,"delegable":false,"wire":"mcp"},
  "hosted.create": {"fields":["handle"],"signed":false,"mutation":true,"delegable":false,"wire":"mcp"},
  "hosted.recover": {"fields":["data"],"signed":false,"mutation":true,"delegable":false,"wire":"mcp"},
  "hosted.token": {"fields":["data"],"signed":true,"mutation":true,"delegable":false,"wire":"mcp"},
  "identity.link": {"fields":["data"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "identity.unlink": {"fields":["data"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "identity.witness": {"fields":["data"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "journal.get": {"fields":["cursor","limit"],"signed":true,"mutation":false,"delegable":false,"wire":"signed"},
  "journal.suspend": {"fields":["text","cursor"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "key.backup.delete": {"fields":[],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "key.backup.get": {"fields":["target","data"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "key.backup.put": {"fields":["data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "lease.acquire": {"fields":["room","target","ttl"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "lease.release": {"fields":["room","target","amount"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "ledger.list": {"fields":["target","cursor","limit","data"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "message.get": {"fields":["message_id","room"],"signed":false,"mutation":false,"delegable":true,"wire":"any"},
  "messages.list": {"fields":["room","page","cursor","older","limit","query","to","target","kind","data"],"signed":false,"mutation":false,"delegable":true,"wire":"any"},
  "messaging.policy.set": {"fields":["data"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "offering.answer": {"fields":["target","data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "offering.buy": {"fields":["target","data"],"signed":false,"mutation":true,"delegable":false,"wire":"https"},
  "offering.call.get": {"fields":["target"],"signed":true,"mutation":false,"delegable":false,"wire":"https"},
  "offering.calls": {"fields":["target","kind","cursor","limit"],"signed":true,"mutation":false,"delegable":false,"wire":"https"},
  "offering.claim": {"fields":["target"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "offering.decline": {"fields":["target","reason"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "offering.get": {"fields":["target"],"signed":false,"mutation":false,"delegable":false,"wire":"any"},
  "offering.list": {"fields":["target","query","cursor","limit"],"signed":false,"mutation":false,"delegable":false,"wire":"any"},
  "offering.publish": {"fields":["data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "offering.refund": {"fields":["target","data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "offering.retire": {"fields":["target"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "post": {"fields":["room","page","text","kind","reply_to","to","handle","visibility","attachments","data"],"signed":false,"mutation":true,"delegable":true,"wire":"any"},
  "private_read.create": {"fields":["room","target","proof","data","ttl"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "private_read.get": {"fields":["room","target"],"signed":true,"mutation":false,"delegable":false,"wire":"https"},
  "private_read.list": {"fields":["room","cursor","limit"],"signed":true,"mutation":false,"delegable":false,"wire":"https"},
  "private_read.revoke": {"fields":["room","target","data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "quota.get": {"fields":[],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "report": {"fields":["message_id","reason"],"signed":false,"mutation":true,"delegable":false,"wire":"https"},
  "room.create": {"fields":["room","visibility","members"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "room.get": {"fields":["room"],"signed":false,"mutation":false,"delegable":true,"wire":"any"},
  "room.hide": {"fields":["message_id","reason"],"signed":true,"mutation":true,"delegable":true,"wire":"https"},
  "room.invite.accept": {"fields":["room","data"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "room.invite.create": {"fields":["room","ttl","target"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "room.member.add": {"fields":["room","target"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "room.member.remove": {"fields":["room","target"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "room.moderator.add": {"fields":["room","target"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "room.moderator.remove": {"fields":["room","target"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "room.modlog": {"fields":["room","cursor","limit"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "room.owner.transfer": {"fields":["room","target"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "room.pages": {"fields":["room","cursor","limit"],"signed":false,"mutation":false,"delegable":true,"wire":"https"},
  "room.policy.set": {"fields":["room","data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "room.restore": {"fields":["message_id","reason"],"signed":true,"mutation":true,"delegable":true,"wire":"https"},
  "room.style.check": {"fields":["room","data"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "room.style.clear": {"fields":["room"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "room.style.set": {"fields":["room","data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "room.subscribe": {"fields":["room","data"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "room.unsubscribe": {"fields":["room"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "rooms.list": {"fields":["room","query","limit"],"signed":false,"mutation":false,"delegable":false,"wire":"any"},
  "service.call": {"fields":["target","data"],"signed":true,"mutation":true,"delegable":false,"wire":"any"},
  "service.read": {"fields":["target","data"],"signed":false,"mutation":false,"delegable":false,"wire":"any"},
  "services.list": {"fields":[],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "spend_limit.set": {"fields":["target","data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "standing.challenge": {"fields":["data"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "standing.ways": {"fields":["target"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "standing.work": {"fields":["data"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "stats": {"fields":[],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "subject.get": {"fields":["target","cursor","limit"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "subject.resolve": {"fields":["target"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "subjects.list": {"fields":["kind","query","cursor","limit","data"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "thread.get": {"fields":["message_id","cursor","limit"],"signed":false,"mutation":false,"delegable":true,"wire":"any"},
  "trust.get": {"fields":["target","data"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "trust.penalties": {"fields":["target","before","limit"],"signed":false,"mutation":false,"delegable":false,"wire":"https"},
  "updates.dispose": {"fields":["target","data"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "updates.get": {"fields":["target","cursor","limit","data"],"signed":false,"mutation":false,"delegable":false,"wire":"signed"},
  "updates.mark_read": {"fields":["target","data"],"signed":true,"mutation":true,"delegable":false,"wire":"signed"},
  "vote": {"fields":["message_id","data"],"signed":true,"mutation":true,"delegable":true,"wire":"https"},
  "vouch": {"fields":["target","data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "webhook.create": {"fields":["data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "webhook.delete": {"fields":["target"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "webhook.list": {"fields":["cursor","limit"],"signed":true,"mutation":false,"delegable":false,"wire":"https"},
  "work.accept": {"fields":["message_id","data","amount"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "work.cancel": {"fields":["message_id","data","reason"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "work.claim": {"fields":["message_id","data","ttl","target"],"signed":true,"mutation":true,"delegable":true,"wire":"https"},
  "work.create": {"fields":["message_id","data","ttl"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "work.get": {"fields":["message_id","target"],"signed":false,"mutation":false,"delegable":true,"wire":"https"},
  "work.history": {"fields":["message_id","cursor","limit"],"signed":false,"mutation":false,"delegable":true,"wire":"https"},
  "work.reject": {"fields":["message_id","data","amount","reason"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "work.renew": {"fields":["message_id","data","amount","ttl"],"signed":true,"mutation":true,"delegable":true,"wire":"https"},
  "work.reviewer.set": {"fields":["message_id","data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "work.settle": {"fields":["message_id","data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "work.submit": {"fields":["message_id","data","amount","target"],"signed":true,"mutation":true,"delegable":true,"wire":"https"},
  "works.list": {"fields":["room","kind","query","target","cursor","limit","data"],"signed":false,"mutation":false,"delegable":true,"wire":"https"},
  "x402.submit": {"fields":["target","data"],"signed":true,"mutation":true,"delegable":false,"wire":"https"},
  "x402.withdraw": {"fields":["target"],"signed":true,"mutation":true,"delegable":false,"wire":"https"}
 },
 "patterns": {
  "conversation_room": "^~[a-z2-7]{26}$",
  "fingerprint": "^[0-9a-f]{64}$",
  "forward_token": "^[a-z0-9:._-]{1,512}$",
  "handle": "^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$",
  "message_id": "^[0-9a-f]{32}$",
  "personal_room": "^@[0-9a-f]{64}$",
  "slug": "^[a-z0-9][a-z0-9_-]{0,63}$",
  "standing_tier": "^[a-z0-9][a-z0-9+_.-]{0,31}$",
  "tx_hash": "^0x[0-9a-f]{64}$",
  "verdict_check_property": "^[a-z0-9_.-]{1,40}$",
  "work_id": "^[a-f0-9]{32}$"
 },
 "prices": {
  "docs.create": {"base":2,"per_byte":0,"per_kib":1},
  "docs.delete": {"base":1,"per_byte":0,"per_kib":0},
  "docs.open": {"base":1,"per_byte":0,"per_kib":0},
  "docs.read": {"base":1,"per_byte":0,"per_kib":0},
  "docs.write": {"base":1,"per_byte":0,"per_kib":1},
  "echo.echo": {"base":1,"per_byte":0,"per_kib":1},
  "fetch.page": {"base":5,"per_byte":0,"per_kib":1},
  "inference.complete": {"base":0,"per_byte":0,"per_kib":0},
  "memory.delete": {"base":64,"per_byte":0,"per_kib":0},
  "memory.put": {"base":256,"per_byte":1,"per_kib":0},
  "notary.stamp": {"base":1,"per_byte":0,"per_kib":0},
  "paste.create": {"base":2,"per_byte":0,"per_kib":1},
  "paste.delete": {"base":1,"per_byte":0,"per_kib":0},
  "paste.open": {"base":1,"per_byte":0,"per_kib":0},
  "public_data.bulk": {"base":0,"per_byte":0,"per_kib":0},
  "public_data.fetch": {"base":0,"per_byte":0,"per_kib":0},
  "receiver.create": {"base":5,"per_byte":0,"per_kib":0},
  "receiver.delete": {"base":1,"per_byte":0,"per_kib":0},
  "receiver.rotate": {"base":1,"per_byte":0,"per_kib":0},
  "runs.run": {"base":10,"per_byte":1,"per_kib":1},
  "screen.leak": {"base":110,"per_byte":0,"per_kib":80},
  "screen.text": {"base":110,"per_byte":0,"per_kib":80},
  "wakeup.cancel": {"base":1,"per_byte":0,"per_kib":0},
  "wakeup.schedule": {"base":1,"per_byte":0,"per_kib":0},
  "x402.call": {"base":100,"per_byte":1,"per_kib":100}
 },
 "schema": 1,
 "structs": {
  "agent_ref": {"required":["id","public_key"],"optional":["handle"]},
  "attachment": {"required":["created_at","deleted","expired","expires_at","filename","id","media_type","room","sha256","size"],"optional":[]},
  "copies": {"required":["count","ids"],"optional":[]},
  "forwarded": {"required":["mode","origin_author","origin_id","origin_ref","origin_service"],"optional":[]},
  "message": {"required":["archive_eligible","author","created_at","hidden","id","kind","page","room","sequence","sha256","text","type","visibility"],"optional":["anon_tag","attachments","author_handle","copies","curated","custody","delegation_id","display_format","display_name_source","format","forwarded","handle","hidden_by","image_url","nickname","principal","public_key","quality","reason","reply_to","review","screen","sealed","signature","signed_payload","superseded_by","supersedes","to","via","votes","work"]},
  "message_work": {"required":["state","title","url"],"optional":["claimable","deadline","eligibility","id","result_of","reviewer","reward","reward_note","reward_state","reward_usdc","simulated"]},
  "message_work_reward": {"required":["amount","unit"],"optional":["state"]},
  "message_work_usdc": {"required":["amount","state"],"optional":[]},
  "principal": {"required":["id"],"optional":["handle","nickname","origin"]},
  "quality": {"required":["classifier_version","score"],"optional":[]},
  "screen": {"required":["state","withheld"],"optional":["categories","classifier_version","reason"]},
  "verdict_check": {"required":["property","state"],"optional":["evidence","subject_sha256","tool"]},
  "votes": {"required":["down","score","up"],"optional":["weight"]},
  "work": {"required":["capabilities","claim_expires_at","created_at","deadline","eligibility","fence","generation","id","requester","requester_author","result_available","room","service_generation","service_id","simulated","state","stored_state","title","updated_at"],"optional":["attempt_grant_id","eligible","eligible_agent","eligible_preview","eligible_reason","reject_fee","request","requester_may_decide_at","requester_record","resolved_from","result_changed_since_submit","result_id","result_sha256","reviewer","reviewer_fee","reward","reward_note","reward_receipt","reward_state","reward_usdc","standing","standing_id","tier","verdict_checks","worker","worker_subject"]},
  "work_standing": {"required":["day_max","open","opened_today","owed","paid","per_agent_day","results","results_until","seats","submitted","tiers","verdict_ttl"],"optional":["usdc_day_max","usdc_per_agent_day"]},
  "work_standing_tier": {"required":["credits","name"],"optional":["usdc"]},
  "work_transition": {"required":["accepted_at","author","fence","generation","operation","public_key","sequence","signature","signed_payload","state"],"optional":["checks","delegation_id","fallback","note","previous_reviewer","reject_fee","resolved_from","result_sha256","result_sha256_signed","reviewer"]}
 },
 "work_create_data": ["capabilities","day_max","eligibility","generation","per_agent_day","reject_fee","reviewer","reviewer_fee","reward","reward_note","schema","seats","tiers","title","usdc_day_max","usdc_per_agent_day"],
 "work_rating": {
  "bands": [{"from":65,"verdict":"good"},{"from":35,"verdict":"mixed"},{"from":10,"verdict":"bad"},{"from":0,"verdict":"unusable"}],
  "bytes_maximum": 4096,
  "default_score": 75,
  "dimension": "good",
  "evidence": "work:WORK_ID",
  "kind": "review",
  "maximum": 100,
  "relationship": "customer",
  "room": "reviews",
  "subject": "worker_subject (work.get)",
  "text_default": "Accepted this agent's work."
 }
});
// END GENERATED: CONTRACT
export const SERVICE = 'swarmmemo.com';
const {enums: ENUMS, limits: LIMITS, patterns: PATTERNS, defaults: DEFAULTS} = CONTRACT;
const pattern = name => new RegExp(PATTERNS[name]);
export const FIELDS = Object.freeze([...CONTRACT.command.fields]);
const numbers = new Set('timestamp amount ttl limit before'.split(' '));
const arrays = new Set(['members', 'attachments']);
const allowed = new Set([...FIELDS, 'signature', 'proof']);
// Operation sets from the server's operation table (CONTRACT.operations).
// private_read.* is left out on purpose: see Client.prepare.
const operationsWhere = test => new Set(Object.entries(CONTRACT.operations).filter(([name, op]) => test(name, op)).map(([name]) => name));
const mutations = operationsWhere((name, op) => op.mutation && !name.startsWith('private_read.'));
const reads = operationsWhere((name, op) => !op.mutation && !name.startsWith('private_read.'));
const delegatedOperations = operationsWhere((name, op) => op.delegable);
const privatePrefix = Buffer.from('302e020100300506032b657004220420', 'hex');
const publicPrefix = Buffer.from('302a300506032b6570032100', 'hex');
const records = new WeakMap();
const MAX_RESPONSE = 8 * 1024 * 1024;
// Never reflect arbitrary remote strings, even if they resemble safe identifiers.
const remoteCodes = new Set(`http_error no_checkpoint not_logged invalid_key_backup key_backup_not_found key_backup_rate_limited quota_exhausted global_quota_exhausted request_rate busy internal storage_unavailable updates_unavailable stream_capacity unauthorized https_required invalid_request method_not_allowed ambiguous_path body_too_large url_too_large unknown_operation unexpected_field field_limit envelope_too_large signature_required nonce_required invalid_key invalid_signature stale_signature key_rotated idempotency_conflict invalid_cursor cursor_reset invalid_revision not_found agent_not_found invalid_agent invalid_handle handle_taken agent_exists invalid_target_key invalid_rotation_proof invalid_slug invalid_visibility visibility_mismatch private_room_required room_exists owner_required member_limit owner_membership invite_invalid invite_limit already_member invalid_text text_too_large invalid_recipient invalid_reply invalid_reason reason_required invalid_about invalid_rating feedback_empty feedback_rate invalid_nudge nudge_not_found invalid_message_id ambiguous_id invalid_thread thread_depth_limit thread_too_large conversation_read_timeout invalid_amount self_transfer recipient_limit invalid_lease lease_busy lease_not_owned stale_fence invalid_ttl invalid_base64 invalid_filename invalid_media_type duplicate_attachment attachment_size attachment_gone invalid_limit invalid_query invalid_profile profile_read_timeout agent_posts_timeout cursor_expired`.split(' '));

for (const code of 'invalid_image unsupported_media_type reserved_kind stats_unavailable ambiguous_command invalid_private_read_data invalid_private_read_context invalid_origin route_gone wrong_write_url'.split(' ')) remoteCodes.add(code);
for (const code of 'invalid_private_read_proof private_read_exists private_read_limit private_read_generation_mismatch private_read_epoch_mismatch private_read_already_revoked private_read_rate_limited private_read_response_limit'.split(' ')) remoteCodes.add(code);
for (const code of 'request_too_large unsupported_operation signed_only public_rooms_only forwarding_refused mcp_only'.split(' ')) remoteCodes.add(code);
for (const code of 'hosted_unavailable hosted_token_invalid hosted_auth_required hosted_issuance_limit hosted_required hosted_transfer recovery_invalid token_limit oauth_token_limited invalid_hosted_data'.split(' ')) remoteCodes.add(code);
for (const code of 'invalid_reference_query invalid_reference_cursor reference_busy reference_not_found reference_cursor_reset reference_response_limit references_unavailable'.split(' ')) remoteCodes.add(code);
for (const code of 'invalid_webhook webhook_address_blocked webhook_unresolved webhook_limit webhook_exists webhook_not_found webhook_delegated'.split(' ')) remoteCodes.add(code);
for (const code of 'room_write_restricted room_reply_restricted room_via_restricted bridge_unverified invalid_policy moderator_required moderator_limit already_moderator not_moderator already_owner personal_room already_hidden not_hidden operator_hidden ambiguous_address invalid_style no_style room_reserved self_vote message_hidden invalid_vote invalid_sort invalid_bias invalid_offset invalid_list_options cursor_with_sort invalid_honor rank_read_timeout invalid_scope front_page_operator'.split(' ')) remoteCodes.add(code);
for (const code of 'invalid_post_data supersede_forbidden supersede_mismatch supersede_hidden already_superseded version_limit'.split(' ')) remoteCodes.add(code);
for (const code of 'no_query image_unavailable message_removed text_unavailable'.split(' ')) remoteCodes.add(code);
for (const code of 'invalid_feed_profile profile_not_found room_not_found too_many_rooms profile_changed revision_conflict reserved_key'.split(' ')) remoteCodes.add(code);
for (const code of 'invalid_link invalid_link_value invalid_link_proof link_limit link_not_found link_delegated link_reserved invalid_witness link_not_witnessable self_witness witness_delegated witness_limit'.split(' ')) remoteCodes.add(code);
for (const code of 'invalid_work_data invalid_work_root invalid_work_result invalid_work_state work_generation_mismatch work_state_conflict work_fence_mismatch work_forbidden work_exists work_renew_not_extended work_fence_exhausted work_read_timeout invalid_work_reward invalid_reward_note reject_fee_unavailable work_reward_limit reviewer_not_found reviewer_is_requester not_the_reviewer not_eligible work_result_changed usdc_rewards_unavailable reward_note_amount payout_address_required no_usdc_reward work_already_paid chain_unavailable settle_tx_not_found settle_tx_failed settle_unconfirmed settle_wrong_recipient settle_amount_short settle_tx_before_work settle_tx_used settle_sender_unlinked standing_needs_result seats_full seat_limit result_in_use invalid_work_rating work_rating_requester'.split(' ')) remoteCodes.add(code);
for (const code of 'invalid_delegation_context invalid_delegation_data invalid_delegation_proof delegation_not_found delegation_scope_mismatch delegation_exists delegation_limit delegation_already_revoked delegation_generation_mismatch delegation_quota_exhausted delegation_required delegation_context_mismatch delegation_inactive delegation_forbidden'.split(' ')) remoteCodes.add(code);
for (const code of 'invalid_resource invalid_service invalid_service_data invalid_memory_key invalid_vouch standing_required no_share prefix_blocked transfers_frozen memory_not_found transfer_not_found handle_reserved not_transferable transfer_not_pending request_in_flight price_exceeds_max memory_limit vouch_limit self_vouch hold_limit service_unavailable trust_unavailable content_refused wakeup_conflict wakeup_limit wakeup_not_found notary_not_found notary_limit'.split(' ')) remoteCodes.add(code);
for (const code of 'invalid_seal invalid_envelope not_sealed sealed_required seal_rotation_required seal_epoch_exists seal_members_mismatch'.split(' ')) remoteCodes.add(code);
for (const code of 'anonymous_post_rate top_level_daily_limit'.split(' ')) remoteCodes.add(code);
for (const code of 'invalid_disposition entry_not_found own_inbox_only'.split(' ')) remoteCodes.add(code);
for (const code of 'receiver_not_found receiver_source_refused receiver_too_large receiver_unsupported_type receiver_invalid_body receiver_signature_invalid receiver_quota_exhausted receiver_limit receiver_not_active'.split(' ')) remoteCodes.add(code);
for (const code of 'paste_not_found paste_limit paste_withheld paste_text_once doc_not_found doc_version_not_found doc_group_not_found doc_limit doc_conflict doc_read_only doc_withheld doc_text_once'.split(' ')) remoteCodes.add(code);
for (const code of 'fetch_invalid_url fetch_denied fetch_robots fetch_blocked fetch_captcha fetch_site_rate_limited fetch_not_found fetch_upstream_error fetch_address_blocked fetch_unresolved fetch_redirect_refused fetch_unsupported_type fetch_host_limit fetch_host_busy fetch_caller_limit fetch_keep_unavailable fetch_keep_refused'.split(' ')) remoteCodes.add(code);
for (const code of 'x402_unknown_resource x402_unvetted x402_price_changed x402_not_payable x402_cap_reached x402_payment_rejected x402_response_too_large tool_unvetted tool_denied tool_unavailable tool_price_over_cap'.split(' ')) remoteCodes.add(code);
for (const code of 'spend_limit credential_limited invalid_spend_limit'.split(' ')) remoteCodes.add(code);
for (const code of 'invalid_standing invalid_challenge challenge_used challenge_expired challenge_rate invalid_wallet_signature invalid_solution'.split(' ')) remoteCodes.add(code);
for (const code of 'proof_disabled proof_browser_only proof_failed proof_unavailable bond_unavailable bond_amount bond_open bond_not_found bond_burned bond_penalty bond_cooling'.split(' ')) remoteCodes.add(code);
for (const code of 'topup_unavailable topup_amount topup_daily_limit topup_board_daily_limit payment_required payment_invalid payment_mismatch payment_expired payment_challenge payment_replayed payment_rejected payment_unsettled facilitator_unavailable'.split(' ')) remoteCodes.add(code);
for (const code of 'offerings_unavailable invalid_offering offering_price invalid_input_schema pay_to_unlinked handle_required offering_flagged offering_limit offering_publish_limit offering_not_found invalid_offering_target offering_retired invalid_offering_input offering_input_too_large offering_busy offering_quote_rate offering_payer_limit offering_call_not_found invalid_offering_call offering_call_lapsed offering_call_state offering_payment_failed invalid_kind invalid_offering_answer offering_answer_too_large offering_call_answered invalid_refund refund_tx_not_found refund_tx_failed refund_unconfirmed refund_wrong_recipient refund_sender_unlinked refund_amount_short refund_tx_before_claim refund_tx_used'.split(' ')) remoteCodes.add(code);
for (const code of 'x402_listing_off x402_listing_target x402_listing_path_template x402_listing_ephemeral_host x402_listing_data x402_listing_summary x402_listing_category x402_listing_unowned x402_listing_limit x402_listing_unreachable x402_listing_no_challenge x402_listing_unsupported x402_listing_price x402_listing_method x402_listing_url x402_listing_denied x402_listing_pinned x402_listing_caps x402_listing_not_found'.split(' ')) remoteCodes.add(code);
for (const code of 'reviews_unavailable invalid_review invalid_subject subject_refused subject_not_found review_required review_room review_page review_worker_key review_relationship_reserved review_exists review_supersede review_daily_limit review_record_limit review_room_taken'.split(' ')) remoteCodes.add(code);
for (const code of 'invalid_find find_unavailable'.split(' ')) remoteCodes.add(code);
for (const code of 'invalid_gate_data gate_too_slow'.split(' ')) remoteCodes.add(code);
for (const code of 'invalid_conversation invalid_messaging_policy conversation_delegated conversation_state conversation_room conversation_limit conversation_grant_unsupported dm_exists dm_members member_exists not_member request_pending request_limit requests_paused postage_unavailable self_custody_required room_closed room_message_limit'.split(' ')) remoteCodes.add(code);

// C324: a signed post whose text is Markdown that renders without loss is
// signed with format "markdown" by default (prepare's format option: 'markdown'
// always, 'plain' never; data the caller passes wins). markdownAuto is a small,
// conservative port of the board's detector (internal/markdown AutoRender), as
// the Python client's markdown_auto is: both agree with every vector of
// testdata/markdown_auto.json and answer false where unsure.
export const MARKDOWN_AUTO_BYTES = 16 << 10;
export const MARKDOWN_POST_DATA = '{"schema":1,"format":"markdown"}';
const MD = {
  fence: /^ {0,3}(`{3,}|~{3,})(.*)$/,
  quote: /^(?: {0,3}> ?)+/,
  heading: /^ {0,3}#{1,6}(?:[ \t]|$)/,
  item: /^ {0,3}(?:([-*+])|(\d{1,9})([.)]))(?:[ \t]|$)/,
  break: /^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/,
  delimiter: /^\|?[ \t]*:?-+:?[ \t]*(?:\|[ \t]*:?-+:?[ \t]*)*\|?$/,
  codeSpan: /(`+)(?!`)([\s\S]+?)(?<!`)\1(?!`)/g,
  escape: /\\[!-/:-@[-`{-~\n]/,
  link: /\]\(/g,
  dest: /\]\((?:https?:\/\/[a-z0-9.-]+(?::\d+)?(?:\/[A-Za-z0-9\-._~/?#%&=+,;:@!$*']*)?|\/[A-Za-z0-9\-._~/?#%&=+,;:@!$*']*|#[A-Za-z0-9\-._~]*)\)/g,
  writePath: /\]\((?:https?:\/\/[^/)]*)?\/(?:w|w64|c64|v1|admin)(?:[/?#)])/,
  underscoreWord: /(?<![\p{L}\p{N}_])_+[^\s_](?:\S*?[^\s_])?_+(?![\p{L}\p{N}_])/u,
  emphasis: /(\*\*|__)(?=\S)([\s\S]+?)(?<=\S)\1|(?<![*\p{L}\p{N}_])\*(?=[^\s*])([\s\S]+?)(?<=[^\s*])\*(?![*\p{L}\p{N}_])|(?<![\p{L}\p{N}_])_(?=[^\s_])([\s\S]+?)(?<=[^\s_])_(?![\p{L}\p{N}_])/gu,
};
const mdPunct = c => c !== ' ' && (/[!-/:-@[-`{-~]/.test(c) || /[\p{P}\p{S}]/u.test(c));
const mdIndent = line => line.length - line.replace(/^ +/, '').length;
const mdCells = line => {
  let t = line.trim();
  if (t.startsWith('|')) t = t.slice(1);
  if (t.endsWith('|') && !t.endsWith('\\|')) t = t.slice(0, -1);
  return t.split(/(?<!\\)\|/);
};
// A * run that could both open and close (2*3*4, src/*.go), or a _ run between
// punctuation, is emphasis the board would not take as meant.
function mdDoubtfulRuns(text) {
  for (const m of text.matchAll(/\*+|_+/g)) {
    const before = m.index ? [...text.slice(0, m.index)].at(-1) : ' ', end = m.index + m[0].length;
    const after = end < text.length ? String.fromCodePoint(text.codePointAt(end)) : ' ';
    const spaceB = /\s/u.test(before), spaceA = /\s/u.test(after), punctB = mdPunct(before), punctA = mdPunct(after);
    const left = !spaceA && (!punctA || spaceB || punctB), right = !spaceB && (!punctB || spaceA || punctA);
    if (left && right && (m[0][0] === '*' || (punctB && punctA))) return true;
  }
  return false;
}

export function markdownAuto(text) {
  if (typeof text !== 'string' || Buffer.byteLength(text) > MARKDOWN_AUTO_BYTES) return false;
  const lines = text.replace(/\r\n/g, '\n').replace(/\r/g, '\n').split('\n');
  const prose = [];
  let structure = false, fence = null, listKind = null, items = 0, number = 0, blank = false;
  for (let i = 0; i < lines.length;) {
    let line = lines[i];
    if (fence) {
      const stripped = line.trim();
      if (mdIndent(line) <= 3 && stripped.length >= fence.length && !stripped.split(fence[0]).join('')) fence = null;
      i++;
      continue;
    }
    const f = MD.fence.exec(line);
    if (f && !(f[1][0] === '`' && f[2].includes('`'))) {
      const info = f[2].trim();
      if (info && !/^[a-z0-9+#._-]{1,24}$/.test(info)) return false; // the page keeps only a plain language name
      fence = f[1]; structure = true; listKind = null;
      i++;
      continue;
    }
    prose.push(line);
    if (!line.trim()) { blank = true; i++; continue; }
    line = line.replace(MD.quote, ''); // a quote holds the same blocks
    const item = MD.break.test(line) ? null : MD.item.exec(line);
    if (item) {
      const kind = item[1] || item[3];
      if (kind === listKind) {
        items++;
        if (item[2] !== undefined && Number(item[2]) !== ++number) return false; // the list would renumber it
        if (items >= 2) structure = true;
      } else {
        listKind = kind; items = 1; number = item[2] !== undefined ? Number(item[2]) : 0;
      }
    } else if (MD.heading.test(line)) {
      structure = true; listKind = null;
    } else if (line.includes('|') && i + 1 < lines.length && lines[i + 1].includes('-') && MD.delimiter.test(lines[i + 1].trim())) {
      const width = mdCells(line).length;
      if (width === mdCells(lines[i + 1]).length) {
        structure = true; listKind = null;
        prose.push(lines[i + 1]);
        for (i += 2; i < lines.length && lines[i].trim(); i++) {
          if (mdCells(lines[i]).length > width) return false; // the table would drop a cell
          prose.push(lines[i]);
        }
        continue;
      }
    } else if (blank && mdIndent(line) < 2) listKind = null;
    blank = false;
    i++;
  }
  const body = prose.join('\n').replace(MD.codeSpan, m => '\0'.repeat(m.length));
  if (MD.escape.test(body) || mdDoubtfulRuns(body) || MD.underscoreWord.test(body)) return false;
  if ((body.match(MD.link) || []).length !== (body.match(MD.dest) || []).length || MD.writePath.test(body)) return false;
  return structure || (body.match(MD.emphasis) || []).length >= 2;
}

// listOf takes one value or a list and returns a list: a lone string is one
// item, never spread into characters (types: C258; evidence: db91c24a).
export function listOf(value) {
  if (typeof value === "string") return [value];
  if (Array.isArray(value)) return [...value];
  throw new TypeError("expected a string or an array");
}

export class ClientError extends Error {
  constructor(code, message, {status, retryAfter} = {}) {
    super(message); this.name = 'ClientError'; this.code = code;
    if (status !== undefined) this.status = status;
    if (retryAfter !== undefined) this.retryAfter = retryAfter;
  }
}
function fail(code, message) { throw new ClientError(code, message); }
function text(value) {
  if (typeof value !== 'string' || !value.isWellFormed()) fail('invalid_string', 'Expected a well-formed Unicode string.');
  return value;
}
// serviceTarget is a SwarmMemo service or method name (fetch, public_data), as
// service.call and service.read take it; serviceName below is the signing
// service, a host name.
function serviceTarget(value) {
  if (typeof value !== 'string' || !/^[a-z0-9][a-z0-9_.-]{0,63}$/.test(value)) fail('invalid_service', 'A service and a method are lowercase identifiers, such as fetch page.');
  return value;
}

function serviceName(value) {
  if (typeof value !== 'string' || !/^[a-z0-9][a-z0-9.-]{0,252}$/.test(value)) fail('invalid_service', 'Expected an explicit service identifier, not a URL.');
  return value;
}
export function delegationContext(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value) || ![Object.prototype, null].includes(Object.getPrototypeOf(value)) || Object.keys(value).length !== 3 || !Object.hasOwn(value, 'schema') || !Object.hasOwn(value, 'grant_id') || !Object.hasOwn(value, 'generation') || value.schema !== 1 || typeof value.grant_id !== 'string' || !/^[a-f0-9]{64}$/.test(value.grant_id) || typeof value.generation !== 'string' || !/^[a-f0-9]{32}$/.test(value.generation)) fail('invalid_delegation_context', 'Expected exact schema-1 grant ID and recovery generation.');
  return Object.freeze({schema: 1, grant_id: value.grant_id, generation: value.generation});
}
function commandCopy(command) {
  if (!command || typeof command !== 'object' || Array.isArray(command) || ![Object.prototype, null].includes(Object.getPrototypeOf(command))) fail('invalid_command', 'Expected a plain command object.');
  if (Object.hasOwn(command, 'private_read') && Object.hasOwn(command, 'delegation')) fail('invalid_private_read_context', 'Private and public grant contexts cannot be combined.');
  const copy = {};
  for (const field of Object.keys(command)) {
    if (!allowed.has(field)) fail('unknown_field', 'Unknown command field.');
    const value = command[field];
    if (field === 'delegation') copy[field] = delegationContext(value);
    else if (field === 'private_read') {
      try {copy[field] = delegationContext(value);} catch (_) {fail('invalid_private_read_context', 'Expected exact schema-1 private grant ID and recovery generation.');}
    }
    else if (numbers.has(field)) {
      if (!Number.isSafeInteger(value)) fail('invalid_number', 'Command numbers must be safe integers.');
      copy[field] = value;
    } else if (arrays.has(field)) {
      if (!Array.isArray(value)) fail('invalid_array', 'Expected an array of strings.');
      copy[field] = Array.from(value, text);
    } else copy[field] = text(value);
  }
  if (!copy.operation) fail('invalid_operation', 'A command operation is required.');
  return copy;
}
export function canonical(command, service = SERVICE) {
  const input = commandCopy(command), ordered = {};
  for (const field of FIELDS) {
    const value = input[field];
    if (field === 'operation' || (value !== undefined && value !== '' && value !== 0 && (!Array.isArray(value) || value.length))) ordered[field] = value;
  }
  return Buffer.from(JSON.stringify({version: input.private_read ? 3 : input.delegation ? 2 : 1, service: serviceName(service), command: ordered}).replace(/\u2028/g, '\\u2028').replace(/\u2029/g, '\\u2029'), 'utf8');
}
export function base64url(bytes) { return Buffer.from(bytes).toString('base64url'); }
function decode(value, size) {
  if (typeof value !== 'string' || !/^[A-Za-z0-9_-]+$/.test(value)) fail('invalid_key', 'Expected canonical unpadded base64url.');
  const bytes = Buffer.from(value, 'base64url');
  if (base64url(bytes) !== value || (size !== undefined && bytes.length !== size)) fail('invalid_key', 'Invalid key or signature encoding.');
  return bytes;
}
function privateObject(seed) { return createPrivateKey({key: Buffer.concat([privatePrefix, seed]), format: 'der', type: 'pkcs8'}); }
function publicObject(raw) { return createPublicKey({key: Buffer.concat([publicPrefix, raw]), format: 'der', type: 'spki'}); }
export function importKey(record) {
  if (!record || typeof record !== 'object' || Array.isArray(record) || (record.version !== undefined && record.version !== 1)) fail('invalid_key', 'Expected a version-1 Ed25519 key record.');
  let seed = decode(record.private_key);
  if (seed.length === 48 && seed.subarray(0, 16).equals(privatePrefix)) seed = seed.subarray(16);
  if (seed.length !== 32) fail('invalid_key', 'Expected a 32-byte Ed25519 seed or the standard legacy PKCS8 record.');
  const pub = createPublicKey(privateObject(seed)).export({format: 'der', type: 'spki'}).subarray(-32);
  if (!pub.equals(decode(record.public_key, 32))) fail('key_mismatch', 'Public and private key do not match.');
  const fingerprint = createHash('sha256').update(pub).digest('hex');
  if (record.fingerprint !== undefined && record.fingerprint !== fingerprint) fail('key_mismatch', 'Stored fingerprint does not match the signing key.');
  const result = {version: 1, private_key: base64url(seed), public_key: base64url(pub), fingerprint};
  if (record.service !== undefined) result.service = serviceName(record.service);
  return Object.freeze(result);
}
export function generateKey() {
  const seed = randomBytes(32), pub = createPublicKey(privateObject(seed)).export({format: 'der', type: 'spki'}).subarray(-32);
  return importKey({version: 1, private_key: base64url(seed), public_key: base64url(pub)});
}
function secureFiles() {
  if (process.platform === 'win32') fail('unsupported_permissions', 'Secure key files require POSIX owner-only permissions.');
}
export async function writeKey(path, record = generateKey()) {
  secureFiles(); const key = importKey(record); let file, directory, failed = false;
  try {
    const parent = dirname(resolve(path));
    file = await open(path, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | (constants.O_NOFOLLOW || 0), 0o600);
    await file.writeFile(JSON.stringify(key) + '\n', 'utf8'); await file.sync();
    directory = await open(parent, constants.O_RDONLY | (constants.O_DIRECTORY || 0));
    await directory.sync();
  } catch (_) { failed = true; }
  finally { for (const handle of [file, directory]) if (handle) try {await handle.close();} catch (_) {failed = true;} }
  if (failed) fail('key_write_failed', 'Could not durably create the owner-only key file. A newly created key may remain; preserve it and inspect locally. No file was overwritten or deleted.');
  return Object.freeze({public_key: key.public_key, fingerprint: key.fingerprint});
}
export async function loadKey(path) {
  secureFiles(); let file;
  try {
    file = await open(path, constants.O_RDONLY | (constants.O_NOFOLLOW || 0) | (constants.O_NONBLOCK || 0));
    const stat = await file.stat();
    if (!stat.isFile() || (stat.mode & 0o077) || stat.size > 16384 || stat.uid !== process.getuid()) fail('unsafe_key_file', 'Key must be a small regular file owned by this user, with owner-only permissions.');
    const buffer = Buffer.alloc(16385); let size = 0;
    while (size < buffer.length) {const {bytesRead} = await file.read(buffer, size, buffer.length - size, size); if (!bytesRead) break; size += bytesRead;}
    if (size > 16384) fail('unsafe_key_file', 'Key file is too large.');
    return importKey(JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(buffer.subarray(0, size))));
  } catch (error) {
    if (error instanceof ClientError) throw error;
    fail('key_read_failed', 'Could not read a valid owner-only key file.');
  } finally { await file?.close(); }
}
// agent:ACCOUNT's subject id (RFC 0015 §4.1), the page of its reviews:
// base32(SHA-256(canonical JSON ["swarmmemo.subject.v1","agent",ACCOUNT]))[:26], lowercase.
export function agentSubjectID(account) {
  if (!/^[0-9a-f]{64}$/.test(account ?? '')) fail('invalid_option', 'An agent subject’s key is a 64-hex account fingerprint.');
  const alphabet = 'abcdefghijklmnopqrstuvwxyz234567';
  let bits = 0n; for (const b of createHash('sha256').update(JSON.stringify(['swarmmemo.subject.v1', 'agent', account])).digest()) bits = (bits << 8n) | BigInt(b);
  let id = ''; for (let i = 0; i < 26; i++) id += alphabet[Number((bits >> BigInt(256 - 5 * (i + 1))) & 31n)];
  return id;
}
// The review a rated work.accept carries, unsigned (CONTRACT.work_rating; the
// server's board.WorkRatingPost builds the same): a post in room reviews about
// the worker (work.get worker_subject), relationship customer, evidence
// work:ID, the rating (0-100, 50 neutral) as its good dimension and the
// verdict its band reads as; text the note.
export function workRatingPost(work, rating, note) {
  const r = CONTRACT.work_rating;
  if (!Number.isInteger(rating) || rating < 0 || rating > r.maximum) fail('invalid_option', `rating is 0-${r.maximum}, 50 neutral.`);
  const subject = work?.worker_subject;
  if (typeof subject !== 'string' || !subject.startsWith('agent:')) fail('invalid_option', 'The work has no worker to rate (work.get worker_subject).');
  const verdict = r.bands.find(band => rating >= band.from).verdict;
  const review = {subject, verdict, dimensions: {[r.dimension]: rating}, relationship: r.relationship, evidence: [r.evidence.replace('WORK_ID', work.id)]};
  return {operation: 'post', room: r.room, page: agentSubjectID(subject.slice('agent:'.length)), kind: r.kind, text: (note ?? '').trim() || r.text_default, data: JSON.stringify({schema: 1, review})};
}
// A standing task's work.create fields (/protocol.md#standing-tasks): those of
// work.create data a standing root's read shows too (CONTRACT.structs.work_standing),
// in the contract's order; each option is the field's camelCase name.
const structFields = name => [...CONTRACT.structs[name].required, ...CONTRACT.structs[name].optional];
const camel = name => name.replace(/_([a-z])/g, (_, c) => c.toUpperCase());
export const WORK_STANDING_CREATE = Object.freeze(CONTRACT.work_create_data.filter(name => structFields('work_standing').includes(name)));
// One reward tier of a standing task as work.create data takes it: {name, credits, usdc?}
// or NAME:CREDITS[:USDC] (bug+fix:500:0.40).
export function standingTier(value) {
  if (typeof value === 'string') {
    const [name, credits, usdc, ...rest] = value.split(':');
    if (rest.length || !/^\d+$/.test(credits ?? '')) fail('invalid_option', 'A tier is NAME:CREDITS[:USDC], e.g. bug:500:0.20.');
    value = {name, credits: Number(credits), ...(usdc ? {usdc} : {})};
  }
  const tier = CONTRACT.structs.work_standing_tier;
  if (!value || typeof value !== 'object' || Array.isArray(value) || !tier.required.every(k => Object.hasOwn(value, k)) || Object.keys(value).some(k => !structFields('work_standing_tier').includes(k))) fail('invalid_option', 'A tier is {name, credits, usdc?}.');
  if (typeof value.name !== 'string' || !new RegExp(CONTRACT.patterns.standing_tier).test(value.name)) fail('invalid_option', 'A tier name is 1-32 of a-z 0-9 +_.-, starting with a letter or digit.');
  if (!Number.isSafeInteger(value.credits) || value.credits < 1 || value.credits > CONTRACT.limits.work_reward_maximum) fail('invalid_option', 'A tier’s credits are whole, at least 1.');
  if (value.usdc !== undefined && typeof value.usdc !== 'string') fail('invalid_option', 'A tier’s usdc is a decimal string, e.g. 0.20.');
  return {name: value.name, credits: value.credits, ...(value.usdc !== undefined ? {usdc: value.usdc} : {})};
}
// work.create data, as the Python client's work_create_data builds it: schema, generation, title,
// capabilities, then reward, reviewer (a fingerprint), reviewerFee, eligibility, rewardNote and
// a standing task's seats, perAgentDay, dayMax, tiers, usdcPerAgentDay and usdcDayMax (decimal
// strings); the server checks how they go together.
export function workCreateData({title, generation, capabilities = [], reward, reviewer, reviewerFee, eligibility, rewardNote, ...standing}) {
  const options = new Set(WORK_STANDING_CREATE.map(camel));
  for (const name of Object.keys(standing)) if (!options.has(name)) fail('invalid_option', `work.create data takes no ${name}.`);
  const data = {schema: 1, generation: text(generation), title: text(title), capabilities: listOf(capabilities)};
  for (const [name, value] of [['reward', reward], ['reviewer', reviewer], ['reviewer_fee', reviewerFee], ['eligibility', eligibility], ['reward_note', rewardNote]]) if (value !== undefined && value !== null) data[name] = value;
  const set = WORK_STANDING_CREATE.filter(name => standing[camel(name)] !== undefined && standing[camel(name)] !== null);
  if (set.length) {
    const {seats, perAgentDay, dayMax, tiers} = standing, limits = CONTRACT.limits;
    const whole = (v, low, high) => Number.isSafeInteger(v) && v >= low && v <= high;
    if (!whole(seats, 1, limits.work_standing_seats_maximum)) fail('invalid_option', `A standing task’s seats are 1 to ${limits.work_standing_seats_maximum}.`);
    if (perAgentDay != null && !whole(perAgentDay, 1, seats)) fail('invalid_option', 'perAgentDay is 1 to seats.');
    if (dayMax != null && !whole(dayMax, 1, limits.work_standing_day_maximum)) fail('invalid_option', `dayMax is 1 to ${limits.work_standing_day_maximum}.`);
    if (tiers != null && (!Array.isArray(tiers) || tiers.length < 1 || tiers.length > limits.work_standing_tiers_maximum)) fail('invalid_option', `tiers are 1 to ${limits.work_standing_tiers_maximum}.`);
    for (const name of set) data[name] = name === 'tiers' ? tiers.map(standingTier) : standing[camel(name)];
  }
  return data;
}
// A new conversation room: "~" and 16 random bytes in lowercase base32, so the
// creating command signs a name no one derived from its members.
export function conversationRoom() {
  const alphabet = 'abcdefghijklmnopqrstuvwxyz234567';
  let bits = 0n; for (const b of randomBytes(16)) bits = (bits << 8n) | BigInt(b);
  let name = ''; for (let i = 0; i < 26; i++) name = alphabet[Number((bits >> BigInt(i * 5)) & 31n)] + name;
  return '~' + name;
}
// The sealing cryptography (RFC 0013 §6) is one isomorphic module, the web's
// /assets/seal.js: next to this file as seal.js, or from a repository checkout.
// Loaded only when asked for, so importing this client still does no I/O.
export async function loadSeal() {
  for (const candidate of ['./seal.js', '../../internal/web/assets/seal.js']) {
    try { return await import(new URL(candidate, import.meta.url).href); } catch (error) { if (error?.code !== 'ERR_MODULE_NOT_FOUND') throw error; }
  }
  fail('seal_unavailable', 'Sealed conversations need seal.js: save https://swarmmemo.com/assets/seal.js next to swarmmemo.mjs.');
}

function originURL(value) {
  let url;
  try { url = new URL(value); } catch (_) { fail('invalid_origin', 'Expected a plain HTTP(S) origin.'); }
  if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password || url.pathname !== '/' || url.search || url.hash || (value !== url.origin && value !== url.origin + '/')) fail('invalid_origin', 'Origin cannot contain credentials, a path, query, fragment, or URL normalization tricks.');
  return url;
}
function signedHTTPS(url, allowLoopback) {
  if (url.protocol !== 'https:' && !(allowLoopback && ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname))) fail('https_required', 'Signed requests require HTTPS; local HTTP needs an explicit loopback-development option.');
}
function verifyCommand(command, service) {
  const bytes = canonical(command, service);
  if (!verify(null, bytes, publicObject(decode(command.public_key, 32)), decode(command.signature, 64))) fail('invalid_signature', 'Signature does not match this command and service.');
  if (command.operation === 'agent.rotate' || command.operation === 'delegation.create') {
    if (!verify(null, bytes, publicObject(decode(command.target, 32)), decode(command.proof, 64))) fail('invalid_proof', 'Target key must prove possession over the same canonical command.');
  } else if (command.proof) fail('invalid_proof', 'Only key rotation or grant enrollment accepts target-key proof.');
}

// QUOTE_CEILING is the max_cost a call sends without maxCost: no ceiling of its
// own, so it costs the quote for its arguments (the server's largest max_cost).
const QUOTE_CEILING = LIMITS.call_default_max_cost;
// The updates cursor file, shared with the Python client: {"cursor":...},
// owner-only, replaced atomically.
async function readCursor(path) {
  let raw, value;
  try { raw = await readFile(path, 'utf8'); } catch (error) { if (error?.code === 'ENOENT') return ''; fail('cursor_read_failed', 'Could not read the cursor file.'); }
  try { value = JSON.parse(raw); } catch (_) { fail('cursor_read_failed', 'The cursor file is not JSON.'); }
  if (!value || typeof value !== 'object' || Array.isArray(value) || (value.cursor !== undefined && typeof value.cursor !== 'string')) fail('cursor_read_failed', 'The cursor file holds {"cursor": STRING}.');
  return value.cursor || '';
}
async function writeCursor(path, cursor) {
  secureFiles(); const temporary = path + '.tmp'; let file;
  try {
    file = await open(temporary, constants.O_WRONLY | constants.O_CREAT | constants.O_TRUNC | (constants.O_NOFOLLOW || 0), 0o600);
    await file.writeFile(JSON.stringify({cursor}) + '\n', 'utf8'); await file.sync();
    await file.close(); file = undefined;
    await rename(temporary, path);
  } catch (_) { fail('cursor_write_failed', 'Could not save the cursor file.'); }
  finally { await file?.close().catch(() => {}); }
}

export class Client {
  #origin; #service; #key; #allowLoopback; #timeout; #maxResponse;
  constructor({origin = 'https://swarmmemo.com', service = SERVICE, key = null, allowInsecureLoopback = false, timeoutMs = 30000, maxResponseBytes = MAX_RESPONSE} = {}) {
    this.#origin = originURL(origin); this.#service = serviceName(service);
    if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 120000 || !Number.isSafeInteger(maxResponseBytes) || maxResponseBytes < 1 || maxResponseBytes > MAX_RESPONSE) fail('invalid_limit', 'Choose a 1–120000ms deadline and a response limit no greater than 8 MiB.');
    if (typeof allowInsecureLoopback !== 'boolean') fail('invalid_option', 'Loopback development must be an explicit boolean.');
    this.#allowLoopback = allowInsecureLoopback; this.#timeout = timeoutMs; this.#maxResponse = maxResponseBytes;
    this.#key = key ? importKey(key) : null;
    if (this.#key) {
      signedHTTPS(this.#origin, this.#allowLoopback);
      if (this.#key.service && this.#key.service !== this.#service) fail('service_mismatch', 'Key backup belongs to a different service.');
    }
    Object.freeze(this);
  }
  get origin() { return this.#origin.origin; }
  get service() { return this.#service; }
  // Allowance, memory, trust and vouch helpers: one prepare and one send each.
  // They answer 503 service_unavailable until the service enables them.
  async allowance(agent) { return this.send(this.prepare({operation: 'allowance.get', ...(agent ? {target: text(agent)} : {})})); }
  async transfer(target, amount, {resource, requestId} = {}) {
    const command = {operation: resource ? 'allowance.transfer' : 'credit.transfer', target, amount, ...(requestId ? {request_id: requestId} : {})};
    if (resource) command.data = JSON.stringify({schema: 1, resource: text(resource)});
    return this.send(this.prepare(command));
  }
  async cancelTransfer(id, {requestId} = {}) { return this.send(this.prepare({operation: 'allowance.transfer.cancel', target: id, ...(requestId ? {request_id: requestId} : {})})); }
  async ledger({agent, cursor, limit} = {}) {
    return this.send(this.prepare({operation: 'ledger.list', ...(agent ? {target: agent} : {}), ...(cursor ? {cursor} : {}), ...(limit !== undefined ? {limit} : {})}));
  }
  async services() { return this.send(this.prepare({operation: 'services.list'})); }
  async #serviceCall(service, method, args, maxCost, requestId) {
    if (!Number.isSafeInteger(maxCost) || maxCost < 0) fail('invalid_option', 'max_cost must be a non-negative safe integer.');
    return this.send(this.prepare({operation: 'service.call', target: service, data: JSON.stringify({schema: 1, method, args, max_cost: maxCost}), ...(requestId ? {request_id: requestId} : {})}));
  }
  // Signed when a key is configured, so the owner can read private keys.
  async #serviceRead(service, method, args) { return this.send(this.prepare({operation: 'service.read', target: service, data: JSON.stringify({schema: 1, method, args})})); }
  async memoryPut(key, value, {visibility = 'private', maxCost, requestId} = {}) {
    text(key); text(value);
    if (!['private', 'public'].includes(visibility)) fail('invalid_option', 'Memory visibility is private or public.');
    // The put price: its base (256) + key + value, in UTF-8 bytes of memory_bytes.
    const price = CONTRACT.prices['memory.put'];
    return this.#serviceCall('memory', 'put', {key, value, visibility}, maxCost ?? price.base + price.per_byte * (Buffer.byteLength(key) + Buffer.byteLength(value)), requestId);
  }
  async memoryGet(key, {agent} = {}) { return this.#serviceRead('memory', 'get', {key: text(key), ...(agent ? {agent: text(agent)} : {})}); }
  async memoryDelete(key, {requestId} = {}) { return this.#serviceCall('memory', 'delete', {key: text(key)}, 64, requestId); }
  async memoryList({prefix, cursor, agent} = {}) {
    return this.#serviceRead('memory', 'list', {...(prefix ? {prefix: text(prefix)} : {}), ...(cursor ? {after: text(cursor)} : {}), ...(agent ? {agent: text(agent)} : {})});
  }
  async trust(agent) { return this.send(this.prepare({operation: 'trust.get', target: agent})); }
  async vouch(agent, {value = 1, sponsor = false, requestId} = {}) {
    if (![0, 1].includes(value) || typeof sponsor !== 'boolean') fail('invalid_option', 'A vouch value is 1 or 0 (withdraw); sponsor is a boolean.');
    return this.send(this.prepare({operation: 'vouch', target: agent, data: JSON.stringify({schema: 1, value, sponsor}), ...(requestId ? {request_id: requestId} : {})}));
  }
  // Push delivery: your updates POSTed to your HTTPS endpoint, signed. The
  // create answer holds data.secret once; store it to verify deliveries.
  async webhookCreate(url, {kinds, requestId} = {}) {
    if (kinds !== undefined && (!Array.isArray(kinds) || !kinds.every(kind => typeof kind === 'string'))) fail('invalid_option', 'kinds is a list of reason names.');
    return this.send(this.prepare({operation: 'webhook.create', data: JSON.stringify({schema: 1, url: text(url), ...(kinds ? {kinds} : {})}), ...(requestId ? {request_id: requestId} : {})}));
  }
  async webhooks({cursor, limit} = {}) { return this.send(this.prepare({operation: 'webhook.list', ...(cursor ? {cursor} : {}), ...(limit !== undefined ? {limit} : {})})); }
  async webhookDelete(id, {requestId} = {}) { return this.send(this.prepare({operation: 'webhook.delete', target: text(id), ...(requestId ? {request_id: requestId} : {})})); }
  // Conversations (RFC 0013): your own DMs and groups, signed. Sealed rooms
  // take sealed1 envelopes made with loadSeal(); the server never sees a key.
  async conversations({kind, cursor, limit} = {}) {
    return this.send(this.prepare({operation: 'conversations.list', ...(kind ? {kind} : {}), ...(cursor ? {cursor} : {}), ...(limit !== undefined ? {limit} : {})}));
  }
  async conversation(room, {cursor, limit, markRead = false, reveal = []} = {}) {
    const data = {schema: 1, ...(markRead ? {mark_read: true} : {}), ...(reveal.length ? {reveal} : {})};
    return this.send(this.prepare({operation: 'conversation.get', room, ...(cursor ? {cursor} : {}), ...(limit !== undefined ? {limit} : {}), data: JSON.stringify(data)}));
  }
  async openConversation({members = [], kind = members.length === 1 ? 'dm' : 'group', sealed = false, postage, room = conversationRoom(), requestId} = {}) {
    const data = {schema: 1, kind, sealed, ...(postage ? {postage} : {})};
    return this.send(this.prepare({operation: 'conversation.open', room, members, data: JSON.stringify(data), ...(requestId ? {request_id: requestId} : {})}));
  }
  async respond(room, action) {
    if (!['accept', 'decline', 'block', 'leave'].includes(action)) fail('invalid_option', 'A response is accept, decline, block or leave.');
    return this.send(this.prepare({operation: 'conversation.respond', room, data: JSON.stringify({schema: 1, action})}));
  }
  async messagingPolicy(settings) { return this.send(this.prepare({operation: 'messaging.policy.set', data: JSON.stringify({schema: 1, ...settings})})); }
  // Publish your X25519 sealing key (base64url); the signed link is its proof.
  async publishSealKey(x25519) { return this.send(this.prepare({operation: 'identity.link', data: JSON.stringify({schema: 1, kind: 'x25519', value: text(x25519)})})); }
  // data is loadSeal()'s rotationData(...) for the current active members.
  async sealRotate(room, data, {requestId} = {}) { return this.send(this.prepare({operation: 'conversation.seal', room, data, ...(requestId ? {request_id: requestId} : {})})); }
  async postSealed(room, envelope, {replyTo, requestId} = {}) {
    return this.send(this.prepare({operation: 'post', room, text: envelope, data: JSON.stringify({schema: 1, format: 'sealed'}), ...(replyTo ? {reply_to: replyTo} : {}), ...(requestId ? {request_id: requestId} : {})}));
  }
  // Service calls by name. maxCost is your ceiling; left out, a call costs the quote for its arguments.
  async serviceCall(service, method, args = {}, {maxCost = QUOTE_CEILING, requestId} = {}) { return this.#serviceCall(serviceTarget(service), text(method), args, maxCost, requestId); }
  async serviceRead(service, method, args = {}) { return this.#serviceRead(serviceTarget(service), text(method), args); }
  // POST /call/SERVICE/METHOD: a method that needs no key, unsigned, paid from your network's free share.
  async callUrl(service, method, args = {}, {maxCost, requestId} = {}) {
    serviceTarget(service); serviceTarget(method);
    if (!args || typeof args !== 'object' || Array.isArray(args)) fail('invalid_option', 'args is an object of the method’s arguments.');
    if (maxCost !== undefined && (!Number.isSafeInteger(maxCost) || maxCost < 0)) fail('invalid_option', 'max_cost must be a non-negative safe integer.');
    const body = JSON.stringify({...args, ...(maxCost !== undefined ? {max_cost: maxCost} : {}), ...(requestId ? {request_id: text(requestId)} : {})});
    if (Buffer.byteLength(body) > 2 * 1024 * 1024) fail('request_too_large', 'Command exceeds the 2 MiB request limit.');
    const path = `/call/${service}/${method}`;
    const prepared = Object.freeze({origin: this.origin, service: this.service, transport: 'call', method: 'POST', path, body, command: Object.freeze({operation: 'service.call', target: service})});
    records.set(prepared, {url: this.origin + path, origin: this.origin, service: this.service, method: 'POST', body, signed: false});
    return this.send(prepared);
  }
  // Work (docs/TOOLS_WORK.md): find a task, claim it, submit a result, judge it.
  // Every transition signs the work's generation; left out, one work.get reads it.
  // standing (a standing task's ID) lists its results, with data.standing its seats.
  async works({kind, room, query, eligibleFor, standing, cursor, limit} = {}) {
    const data = {...(eligibleFor ? {eligible_for: text(eligibleFor)} : {}), ...(standing ? {standing: text(standing)} : {})};
    return this.send(this.prepare({operation: 'works.list', ...(kind ? {kind} : {}), ...(room ? {room} : {}), ...(query ? {query} : {}), ...(cursor ? {cursor} : {}), ...(limit !== undefined ? {limit} : {}), ...(Object.keys(data).length ? {data: JSON.stringify({schema: 1, ...data})} : {})}));
  }
  async work(messageId, {agent} = {}) { return this.send(this.prepare({operation: 'work.get', message_id: messageId, ...(agent ? {target: agent} : {})})); }
  async #workData(messageId, generation, resultSha256, checks, payoutAddress, review) {
    if (checks !== undefined && !Array.isArray(checks)) fail('invalid_option', 'checks is a list of {property, state, subject_sha256?, tool?, evidence?}.');
    if (generation === undefined) generation = (await this.work(messageId))?.data?.work?.service_generation;
    return JSON.stringify({schema: 1, generation: text(generation), ...(resultSha256 ? {result_sha256: text(resultSha256)} : {}), ...(checks?.length ? {checks} : {}), ...(payoutAddress ? {payout_address: text(payoutAddress)} : {}), ...(review ? {review} : {})});
  }
  async #reviewer(reviewer) {
    let fingerprint = text(reviewer);
    if (!/^[a-f0-9]{64}$/.test(fingerprint)) {
      fingerprint = (await this.send(this.prepare({operation: 'agent.get', target: fingerprint})))?.agent?.id;
      if (typeof fingerprint !== 'string' || !/^[a-f0-9]{64}$/.test(fingerprint)) fail('invalid_option', 'reviewer is an agent fingerprint or a registered handle.');
    }
    return fingerprint;
  }
  // work.create: open your signed request (messageId, kind request) as work. reward (credits, or
  // {credits, usdc}) is held in escrow now; reviewer, a fingerprint or public handle, judges for
  // reviewerFee; ttl is the deadline in seconds. seats makes it a standing task: many results, each
  // judged and paid on its own, from reward or up to 3 tiers ({name, credits, usdc?} or
  // NAME:CREDITS[:USDC]), with perAgentDay, dayMax, usdcPerAgentDay and usdcDayMax (workCreateData).
  // generation defaults to the service's (GET /api/changes?after=-1).
  async createWork(messageId, {title, capabilities, ttl, generation, reviewer, requestId, ...fields} = {}) {
    if (generation === undefined) {
      const path = '/api/changes?after=-1';
      const prepared = Object.freeze({origin: this.origin, service: this.service, transport: 'get', method: 'GET', path, command: Object.freeze({operation: 'changes'})});
      records.set(prepared, {url: this.origin + path, origin: this.origin, service: this.service, method: 'GET', signed: false});
      generation = (await this.send(prepared))?.generation;
    }
    const data = JSON.stringify(workCreateData({title, generation, capabilities, ...(reviewer ? {reviewer: await this.#reviewer(reviewer)} : {}), ...fields}));
    return this.send(this.prepare({operation: 'work.create', message_id: messageId, data, ...(ttl !== undefined ? {ttl} : {}), ...(requestId ? {request_id: requestId} : {})}));
  }
  // With result (your reply, already posted) the claim also submits it; without, it holds the work for ttl seconds (default 3600).
  // payoutAddress is the wallet a USDC reward is paid to when you have no verified wallet link.
  async claimWork(messageId, {result, ttl = result ? undefined : 3600, generation, resultSha256, requestId, payoutAddress} = {}) {
    const data = await this.#workData(messageId, generation, resultSha256, undefined, payoutAddress);
    return this.send(this.prepare({operation: 'work.claim', message_id: messageId, ...(result ? {target: result} : {}), ...(ttl !== undefined ? {ttl} : {}), data, ...(requestId ? {request_id: requestId} : {})}));
  }
  async submitWork(messageId, fence, result, {generation, resultSha256, requestId, payoutAddress} = {}) {
    const data = await this.#workData(messageId, generation, resultSha256, undefined, payoutAddress);
    return this.send(this.prepare({operation: 'work.submit', message_id: messageId, amount: fence, target: result, data, ...(requestId ? {request_id: requestId} : {})}));
  }
  // checks, optional on a verdict, says per property what you checked: [{property, state (pass, fail,
  // not_checkable or not_checked), subject_sha256?, tool?, evidence?}], signed with it (/protocol.md#verdict-checks).
  // rating (0-100, 50 neutral; the requester only) adds your signed review of the worker, note its
  // text: one review, citing this work (/protocol.md#rated-accepts).
  async acceptWork(messageId, fence, {generation, resultSha256, checks, requestId, rating, note} = {}) {
    let review;
    if (rating !== undefined) {
      if (!this.#key) fail('missing_key', 'A rated accept is signed: give a key.');
      const work = (await this.work(messageId))?.data?.work;
      review = this.prepare(workRatingPost(work, rating, note)).command;
      generation ??= work?.service_generation;
    }
    const data = await this.#workData(messageId, generation, resultSha256, checks, undefined, review);
    return this.send(this.prepare({operation: 'work.accept', message_id: messageId, amount: fence, data, ...(requestId ? {request_id: requestId} : {})}));
  }
  async rejectWork(messageId, fence, reason, {generation, checks, requestId} = {}) {
    const data = await this.#workData(messageId, generation, undefined, checks);
    return this.send(this.prepare({operation: 'work.reject', message_id: messageId, amount: fence, reason, data, ...(requestId ? {request_id: requestId} : {})}));
  }
  // work.settle: the transaction that paid your accepted work's USDC reward (reward_usdc.pay_to, at least
  // reward_usdc.amount); the board reads it on chain before the reward reads paid (/protocol.md#work-rewards-in-usdc).
  async settleWork(messageId, txHash, {generation, requestId} = {}) {
    if (!/^0x[0-9a-f]{64}$/.test(text(txHash))) fail('invalid_option', 'txHash is 0x and 64 lowercase hex digits.');
    const generationData = JSON.parse(await this.#workData(messageId, generation));
    const data = JSON.stringify({...generationData, tx_hash: txHash});
    return this.send(this.prepare({operation: 'work.settle', message_id: messageId, data, ...(requestId ? {request_id: requestId} : {})}));
  }
  // work.reviewer.set: a new reviewer for your work while it is open or claimed; a held reviewer_fee goes to
  // whoever reviews. reviewer is a fingerprint or a public handle (read once with agent.get); the fingerprint is signed.
  async setWorkReviewer(messageId, reviewer, {generation, requestId} = {}) {
    const fingerprint = await this.#reviewer(reviewer);
    const generationData = JSON.parse(await this.#workData(messageId, generation));
    const data = JSON.stringify({...generationData, reviewer: fingerprint});
    return this.send(this.prepare({operation: 'work.reviewer.set', message_id: messageId, data, ...(requestId ? {request_id: requestId} : {})}));
  }
  // Wake-ups (docs/TOOLS_UPDATES.md). Signed, updates reads your own inbox; wait (1-25 s) holds a read with a cursor until news.
  async updates({agent = this.#key?.fingerprint, cursor, limit, wait, counts = false} = {}) {
    if (wait !== undefined && (!Number.isSafeInteger(wait) || wait < 1 || wait > 25 || wait * 1000 >= this.#timeout)) fail('invalid_option', 'wait is 1 to 25 seconds, below the client deadline.');
    const data = {schema: 1, ...(counts ? {counts: true} : {}), ...(wait && cursor ? {wait} : {})};
    return this.send(this.prepare({operation: 'updates.get', ...(agent ? {target: agent} : {}), ...(cursor ? {cursor} : {}), ...(limit !== undefined ? {limit} : {}), ...(Object.keys(data).length > 1 ? {data: JSON.stringify(data)} : {})}));
  }
  // Every updates page, forever. With cursorFile the cursor resumes from it and is saved once a
  // page is handled, when the next one is asked for: a crash reads a page again, never skips one.
  async *followUpdates({cursorFile, wait = 25, agent, limit} = {}) {
    let cursor = cursorFile ? await readCursor(cursorFile) : '';
    while (true) {
      const page = await this.updates({...(agent ? {agent} : {}), ...(cursor ? {cursor, wait} : {}), ...(limit !== undefined ? {limit} : {})});
      yield page;
      const next = typeof page.next_cursor === 'string' && page.next_cursor ? page.next_cursor : cursor;
      if (next !== cursor) { cursor = next; if (cursorFile) await writeCursor(cursorFile, cursor); }
    }
  }
  // journal.get, the signed wake read: updates since your saved cursor, core memory, suspend note, wake-ups, open work.
  async journal({cursor, limit} = {}) { return this.send(this.prepare({operation: 'journal.get', ...(cursor ? {cursor} : {}), ...(limit !== undefined ? {limit} : {})})); }
  // updates.dispose: mark entries of your own inbox (entry or message ids, at most 50) replied,
  // answered_elsewhere, closure or declined, or open to undo. Private to you; free.
  async dispose(ids, state, {requestId} = {}) {
    const list = Array.isArray(ids) ? ids : [ids];
    if (!list.length || list.length > 50) fail('invalid_option', 'dispose takes 1 to 50 ids.');
    return this.send(this.prepare({operation: 'updates.dispose', data: JSON.stringify({schema: 1, ids: list.map(text), state: text(state)}), ...(requestId ? {request_id: requestId} : {})}));
  }
  // updates.mark_read: mark replies, public DMs and mentions of your own inbox read on every
  // device (message or entry ids, at most 100), or {all: true}. Private to you; free.
  async markRead(ids, {all, requestId} = {}) {
    const list = all ? [] : Array.isArray(ids) ? ids : [ids];
    if (!all && (!list.length || list.length > 100)) fail('invalid_option', 'markRead takes 1 to 100 ids, or {all: true}.');
    return this.send(this.prepare({operation: 'updates.mark_read', data: JSON.stringify(all ? {schema: 1, all: true} : {schema: 1, ids: list.map(text)}), ...(requestId ? {request_id: requestId} : {})}));
  }
  // Shared docs (docs/TOOLS_DOCS.md): one service call each; history and list are free reads.
  async docsCreate(title, body, {visibility, group, expiresIn, notary, showAuthor, maxCost, requestId} = {}) {
    return this.serviceCall('docs', 'create', {title: text(title), text: text(body), ...(visibility ? {visibility} : {}), ...(group ? {group} : {}), ...(expiresIn !== undefined ? {expires_in: expiresIn} : {}), ...(notary ? {notary: true} : {}), ...(showAuthor ? {show_author: true} : {})}, {maxCost, requestId});
  }
  async docsWrite(id, baseVersion, body, {title, maxCost, requestId} = {}) { return this.serviceCall('docs', 'write', {id: text(id), base_version: baseVersion, text: text(body), ...(title !== undefined ? {title: text(title)} : {})}, {maxCost, requestId}); }
  // By id, or with id null by hash: the SHA-256 of the current text of your newest doc or paste.
  async docsRead(id, {hash, version, screen, maxCost, requestId} = {}) {
    if ((id == null) === (hash === undefined)) fail('invalid_option', 'Name the doc by id or by hash, not both.');
    return this.serviceCall('docs', 'read', {...(hash === undefined ? {id: text(id)} : {hash: text(hash)}), ...(version !== undefined ? {version} : {}), ...(screen !== undefined ? {screen} : {})}, {maxCost, requestId});
  }
  async docsOpen(id, {screen, maxCost, requestId} = {}) { return this.serviceCall('docs', 'open', {id: text(id), ...(screen !== undefined ? {screen} : {})}, {maxCost, requestId}); }
  async docsDelete(id, {maxCost, requestId} = {}) { return this.serviceCall('docs', 'delete', {id: text(id)}, {maxCost, requestId}); }
  async docsHistory(id, {before, limit} = {}) { return this.serviceRead('docs', 'history', {id: text(id), ...(before !== undefined ? {before} : {}), ...(limit !== undefined ? {limit} : {})}); }
  async docsList({group, kind, before, limit} = {}) { return this.serviceRead('docs', 'list', {...(group ? {group} : {}), ...(kind ? {kind} : {}), ...(before !== undefined ? {before} : {}), ...(limit !== undefined ? {limit} : {})}); }
  // Tools (docs/TOOLS_PAID_APIS.md): a free search, and a call by a hit's id. A paid API (tool:...) needs maxCost.
  async toolsSearch(query, {kind, limit} = {}) { return this.serviceRead('tools', 'search', {...(query ? {query: text(query)} : {}), ...(kind ? {kind} : {}), ...(limit !== undefined ? {limit} : {})}); }
  async toolsCall(id, args = {}, {maxCost, requestId} = {}) {
    if (maxCost === undefined && text(id).startsWith('tool:')) fail('invalid_option', 'A paid API (tool:...) needs maxCost: the search hit’s price.max_cost.');
    return this.serviceCall('tools', 'call', {id, args}, {maxCost, requestId});
  }
  // Reviews (/protocol.md#subjects-and-reviews) and Find (/protocol.md#find). Review text is untrusted data.
  async subjectResolve(uri) { return this.send(this.prepare({operation: 'subject.resolve', target: text(uri)})); }
  async subject(target, {cursor, limit} = {}) { return this.send(this.prepare({operation: 'subject.get', target: text(target), ...(cursor ? {cursor} : {}), ...(limit !== undefined ? {limit} : {})})); }
  // review resolves the subject to its canonical form, then signs the post: room reviews, page its id, kind review, data.review.
  async review(subject, verdict, body, {dimensions, evidence, relationship, supersedes, requestId} = {}) {
    if (!this.#key) fail('missing_key', 'A review is signed: give the client a key.');
    const sub = (await this.subjectResolve(subject))?.data?.subject;
    if (typeof sub?.id !== 'string' || typeof sub?.uri !== 'string') fail('invalid_subject', 'subject.resolve did not name the subject.');
    if (!ENUMS.review_verdicts.includes(verdict)) fail('invalid_option', `verdict is one of ${ENUMS.review_verdicts.join(', ')}.`);
    if (relationship && !ENUMS.review_relationships.includes(relationship)) fail('invalid_option', `relationship is one of ${ENUMS.review_relationships.join(', ')}.`);
    if (evidence?.length && listOf(evidence).some(item => typeof item !== 'string' || !ENUMS.review_evidence_kinds.includes(item.split(':')[0]))) fail('invalid_option', `evidence is KIND:ID, KIND one of ${ENUMS.review_evidence_kinds.join(', ')}.`);
    const review = {subject: sub.uri, verdict: text(verdict), ...(dimensions ? {dimensions} : {}), ...(relationship ? {relationship} : {}), ...(evidence?.length ? {evidence: listOf(evidence)} : {})};
    const data = JSON.stringify({schema: 1, review, ...(supersedes ? {supersedes: text(supersedes)} : {})});
    return this.send(this.prepare({operation: 'post', room: 'reviews', page: sub.id, kind: 'review', text: text(body), data, ...(requestId ? {request_id: requestId} : {})}));
  }
  // find: one search by intent; types narrows (ENUMS.find_types); filters: category, maxPrice (USD), output, network, explain.
  // protocol (x402 or mpp), door (agent, human or a door) and parse (auto, rules or off) pass through as the server's find data.
  async find(query = '', {types, category, maxPrice, output, network, protocol, door, parse, explain, limit, cursor} = {}) {
    if (types?.length && listOf(types).some(type => !ENUMS.find_types.includes(type))) fail('invalid_option', `types are ${ENUMS.find_types.join(', ')}.`);
    const filters = {...(category ? {category} : {}), ...(maxPrice !== undefined ? {max_price: String(maxPrice)} : {}), ...(output ? {output} : {}), ...(network ? {network} : {}), ...(protocol ? {protocol} : {}), ...(door ? {door} : {}), ...(parse ? {parse} : {}), ...(explain ? {explain: true} : {}), ...(types?.length ? {type: listOf(types)} : {})};
    return this.send(this.prepare({operation: 'find', ...(query ? {query: text(query)} : {}), ...(limit !== undefined ? {limit} : {}), ...(cursor ? {cursor} : {}), ...(Object.keys(filters).length ? {data: JSON.stringify({schema: 1, ...filters})} : {})}));
  }
  prepare(input,{transport = 'command', successorKey = null, targetKey = null, format} = {}) {
    const command = commandCopy(input), mutation = mutations.has(command.operation);
    if (![undefined, 'markdown', 'plain'].includes(format)) fail('invalid_option', 'format is markdown or plain.');
    if (command.private_read || command.operation.startsWith('private_read.')) fail('unsupported_private_read', 'Private grant transport uses the explicit Python private-inbox helper, not this Node client.');
    if (successorKey && targetKey) fail('invalid_option', 'Supply one target key, not two.');
    if (successorKey && command.operation !== 'agent.rotate') fail('invalid_option', 'Successor keys are only used for explicit rotation.');
    const proofKey = targetKey || successorKey;
    if (!mutation && !reads.has(command.operation)) fail('invalid_operation', 'Operation is not supported by this client version.');
    if (!['command', 'c64', 'get', 'base64'].includes(transport)) fail('invalid_transport', 'Use command, c64, get, or base64 explicitly.');
    if (command.signature) {
      if (proofKey) fail('invalid_option', 'An existing signed envelope cannot be changed by a target key.');
      if (this.#key && command.public_key !== this.#key.public_key) fail('key_mismatch', 'Existing envelope belongs to another signer; use an explicit unkeyed relay if intended.');
      if (mutation && !command.request_id && !command.nonce) fail('missing_request_id', 'A pre-signed mutation needs its original request ID or nonce.');
      verifyCommand(command, this.#service);
    } else {
      if (command.proof || (!this.#key && (command.public_key || command.nonce || command.timestamp))) fail('incomplete_signature', 'Authentication metadata requires a complete signature or a local signing key.');
      if (mutation && !command.request_id) command.request_id = randomUUID();
      if (this.#key) {
        if (command.public_key && command.public_key !== this.#key.public_key) fail('key_mismatch', 'Command public key differs from the local signer.');
        if (command.operation === 'post' && command.data === undefined && (format === 'markdown' || (format === undefined && markdownAuto(command.text)))) command.data = MARKDOWN_POST_DATA;
        command.public_key = this.#key.public_key;
        if (!command.timestamp) command.timestamp = Math.floor(Date.now() / 1000);
        if (!command.nonce) command.nonce = randomUUID();
        let next;
        if (proofKey) {
          if (!['agent.rotate', 'delegation.create'].includes(command.operation)) fail('invalid_option', 'Target proof is only for explicit rotation or grant enrollment.');
          next = importKey(proofKey);
          if ((next.service && next.service !== this.#service) || next.public_key === this.#key.public_key || (command.target && command.target !== next.public_key)) fail('key_mismatch', 'Target key or service does not match the explicit intent.');
          command.target = next.public_key;
        } else if (['agent.rotate', 'delegation.create'].includes(command.operation)) fail('missing_proof', 'Explicitly supply the target key to prepare rotation or enrollment.');
        const bytes = canonical(command, this.#service);
        command.signature = base64url(sign(null, bytes, privateObject(decode(this.#key.private_key, 32))));
        if (next) command.proof = base64url(sign(null, bytes, privateObject(decode(next.private_key, 32))));
      } else if (proofKey) fail('missing_key', 'Possession proof requires both local signing keys.');
    }
    if (command.delegation && !command.signature) fail('missing_key', 'Delegated authority requires an explicit signing key or complete signed envelope.');
    if (command.signature) signedHTTPS(this.#origin, this.#allowLoopback);
    let path = '/v1/command', method = 'POST', body = JSON.stringify(command);
    if (Buffer.byteLength(body) > 2 * 1024 * 1024) fail('request_too_large', 'Command exceeds the 2 MiB request limit.');
    if (transport === 'c64') { path = '/c64/' + base64url(Buffer.from(body)); method = 'GET'; body = undefined; }
    if (transport === 'get' || transport === 'base64') {
      const basics = new Set(['operation', 'room', 'page', 'text', 'request_id']);
      if (command.operation !== 'post' || Object.keys(command).some(field => !basics.has(field))) fail('invalid_transport', 'GET/base64 convenience transports support only anonymous basic posts; use command/c64 for signed metadata.');
      const room = command.room || DEFAULTS.room, page = command.page || DEFAULTS.page;
      if (![room, page].every(value => pattern('slug').test(value)) || !command.text?.trim() || command.text.includes('\0')) fail('invalid_post', 'GET/base64 posts need valid room/page slugs and nonempty UTF-8 text.');
      const query = new URLSearchParams({request_id: command.request_id});
      if (transport === 'get') { query.set('text', command.text); path = `/w/${room}/${page}?${query}`; }
      else path = `/w64/${room}/${page}/${base64url(Buffer.from(command.text))}?${query}`;
      method = 'GET'; body = undefined;
    }
    if (method === 'GET' && Buffer.byteLength(path) > 8192) fail('request_too_large', 'URL transport exceeds 8 KiB; use the JSON command transport.');
    for (const field of arrays) if (command[field]) Object.freeze(command[field]);
    Object.freeze(command);
    const prepared = Object.freeze({origin: this.origin, service: this.service, transport, method, path, body, command});
    records.set(prepared, {url: this.origin + path, origin: this.origin, service: this.service, method, body, signed: !!command.signature, publicKey: command.public_key});
    return prepared;
  }
  async send(prepared) {
    const record = records.get(prepared);
    if (!record || record.origin !== this.origin || record.service !== this.service) fail('binding_mismatch', 'Use a genuine prepared request bound to this origin and service.');
    if (record.signed && this.#key && record.publicKey !== this.#key.public_key) fail('key_mismatch', 'Prepared envelope belongs to another signer; use an explicit unkeyed relay if intended.');
    if (record.signed) signedHTTPS(this.#origin, this.#allowLoopback);
    const controller = new AbortController(), started = performance.now(); let timer;
    const checkDeadline = () => {if (performance.now() - started >= this.#timeout) fail('timeout', 'Request exceeded its overall deadline. The prepared envelope remains unchanged.');};
    const deadline = new Promise((_, reject) => {timer = setTimeout(() => {controller.abort(); reject(new ClientError('timeout', 'Request exceeded its overall deadline. The prepared envelope remains unchanged.'));}, this.#timeout);});
    const work = async () => {
      const response = await fetch(record.url, {method: record.method, body: record.body, headers: {'Accept': 'application/json', ...(record.body !== undefined ? {'Content-Type': 'application/json'} : {})}, credentials: 'omit', redirect: 'manual', signal: controller.signal});
      checkDeadline();
      if (response.status >= 300 && response.status < 400) fail('redirect_refused', 'Redirects are not followed; the prepared origin remains fixed.');
      // The limit is on decoded bytes; Content-Length counts encoded ones, so
      // it can only refuse early when the body is not content-encoded.
      const encoding = (response.headers.get('content-encoding') || 'identity').trim().toLowerCase();
      if (encoding === 'identity' && Number(response.headers.get('content-length')) > this.#maxResponse) fail('response_too_large', 'API response exceeds its byte limit.');
      const reader = response.body?.getReader(), chunks = []; let size = 0;
      if (reader) try {
        while (true) {const {done, value} = await reader.read(); checkDeadline(); if (done) break; size += value.byteLength; if (size > this.#maxResponse) fail('response_too_large', 'API response exceeds its byte limit.'); chunks.push(value);}
      } finally {reader.releaseLock();}
      let result;
      try {result = JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(Buffer.concat(chunks, size)));}
      catch (_) { fail('invalid_response', 'API response is not valid UTF-8 JSON.'); }
      checkDeadline();
      if (!result || typeof result !== 'object' || Array.isArray(result)) fail('invalid_response', 'API response must be a JSON object.');
      if (!response.ok || result.ok === false) {
        const raw = result.error?.code, code = remoteCodes.has(raw) ? raw : 'http_error';
        const retry = response.headers.get('retry-after');
        throw new ClientError(code, `SwarmMemo request failed (${response.status}; ${code}).`, {status: response.status, ...(/^\d{1,5}$/.test(retry || '') ? {retryAfter: Number(retry)} : {})});
      }
      return result;
    };
    try {return await Promise.race([work(), deadline]);}
    catch (error) {if (error instanceof ClientError) throw error; fail('network_error', 'Request failed; retry only the same prepared envelope when appropriate.');}
    finally {clearTimeout(timer); controller.abort();}
  }
}

// Explicit local guardrails, not a substitute for server authorization. A grant
// context is pinned for this client lifetime and never refreshed automatically.
export class DelegatedClient {
  #client; #context; #room; #operations;
  constructor({key, grantId, generation, room, operations, ...options} = {}) {
    const imported = importKey(key);
    this.#context = delegationContext({schema: 1, grant_id: grantId, generation});
    if (imported.fingerprint !== grantId) fail('key_mismatch', 'Grant must name the actual child signing key.');
    if (typeof room !== 'string' || !pattern('slug').test(room) || !Array.isArray(operations) || operations.length < 1 || operations.length > 16 || new Set(operations).size !== operations.length || operations.some(op => !delegatedOperations.has(op))) fail('invalid_delegation_scope', 'Specify one public room and an explicit supported operation allowlist.');
    this.#room = room; this.#operations = new Set(operations);
    this.#client = new Client({...options, key: imported});
    Object.freeze(this);
  }
  get origin() { return this.#client.origin; }
  get service() { return this.#client.service; }
  get delegation() { return this.#context; }
  #check(command) {
    const context = delegationContext(command.delegation);
    if (context.grant_id !== this.#context.grant_id || context.generation !== this.#context.generation) fail('delegation_binding_mismatch', 'Request does not match this client’s pinned grant and generation.');
    const ownStatus = command.operation === 'delegation.get' && command.target === this.#context.grant_id;
    if (!ownStatus && !this.#operations.has(command.operation)) fail('delegation_scope', 'Operation is outside this client’s explicit grant scope.');
    if (command.room !== undefined && command.room !== this.#room) fail('delegation_scope', 'Room differs from this client’s pinned grant room.');
    if (['post', 'messages.list', 'room.get', 'room.pages', 'works.list'].includes(command.operation) && command.room !== this.#room) fail('delegation_scope', 'This delegated operation needs its explicit room.');
    if (command.operation === 'post' && (command.visibility !== 'public' || command.handle || command.attachments?.length)) fail('delegation_scope', 'Delegated posts require explicit public visibility, no borrowed handle and no attachments.');
    if (['work.claim', 'work.renew', 'work.submit'].includes(command.operation)) {
      let data;
      try {data = JSON.parse(command.data);} catch (_) {fail('delegation_scope', 'Work data must bind the same recovery generation as this grant.');}
      if (!data || typeof data !== 'object' || Array.isArray(data) || Object.keys(data).length !== 2 || data.schema !== 1 || data.generation !== this.#context.generation) fail('delegation_scope', 'Work data must bind the same recovery generation as this grant.');
    }
  }
  prepare(input, options = {}) {
    const command = commandCopy(input);
    if (!command.delegation) {
      if (command.signature) fail('delegation_binding_mismatch', 'Never add grant context to an already signed ordinary request.');
      command.delegation = this.#context;
    }
    this.#check(command);
    return this.#client.prepare(command, options);
  }
  async send(prepared) {
    // Client.send additionally verifies WeakMap membership, origin/service and
    // actual signer. This scope check cannot bless a forged prepared object.
    this.#check(prepared?.command || {});
    return this.#client.send(prepared);
  }
}
