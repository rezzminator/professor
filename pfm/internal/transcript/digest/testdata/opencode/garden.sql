-- An invented OpenCode store: one session with a child, two sessions sharing a
-- title, one without messages. T0 = 2026-09-30T10:00:00Z = 1790762400000 ms.
INSERT INTO project VALUES ('prj_garden', '/work/garden', 'git', 'garden', 1790762400000, 1790762400000, '[]');
INSERT INTO session VALUES ('ses_1a2b3c4d5e6fGarden0001', 'prj_garden', NULL, 'tidy-garden', '/work/garden',
  'Tidy the garden beds', '1.14.30', 1790762400000, 1790762467000, NULL);
INSERT INTO session VALUES ('ses_9z8y7x6w5v4uSurvey0002', 'prj_garden', 'ses_1a2b3c4d5e6fGarden0001', 'survey-beds',
  '/work/garden', 'Survey the beds (@explore subagent)', '1.14.30', 1790762408000, 1790762410000, NULL);
INSERT INTO session VALUES ('ses_twin0000000000000000A1', 'prj_garden', NULL, 'twin-a', '/work/garden',
  'Twin title', '1.14.30', 1790766000000, 1790766000000, NULL);
INSERT INTO session VALUES ('ses_twin0000000000000000B2', 'prj_garden', NULL, 'twin-b', '/work/garden',
  'Twin title', '1.14.30', 1790769600000, 1790769600000, NULL);
INSERT INTO session VALUES ('ses_empty00000000000000003', 'prj_garden', NULL, 'empty', '/work/garden',
  'Empty one', '1.14.30', 1790773200000, 1790773200000, NULL);

INSERT INTO message VALUES ('msg_01', 'ses_1a2b3c4d5e6fGarden0001', 1790762400000, 1790762400000,
  '{"role":"user","time":{"created":1790762400000},"agent":"build","model":{"providerID":"openai","modelID":"gpt-5.6-sol","variant":"high"}}');
INSERT INTO part VALUES ('prt_01a', 'msg_01', 'ses_1a2b3c4d5e6fGarden0001', 1790762400000, 1790762400000,
  '{"type":"text","text":"Tidy the beds and list what changed"}');
INSERT INTO part VALUES ('prt_01b', 'msg_01', 'ses_1a2b3c4d5e6fGarden0001', 1790762400000, 1790762400000,
  '{"type":"file","mime":"text/plain","filename":"notes.txt","url":"file:///work/garden/notes.txt"}');
INSERT INTO part VALUES ('prt_01c', 'msg_01', 'ses_1a2b3c4d5e6fGarden0001', 1790762400000, 1790762400000,
  '{"type":"text","synthetic":true,"text":"Called the Read tool with the following input: {\"filePath\":\"/work/garden/notes.txt\"}"}');

INSERT INTO message VALUES ('msg_02', 'ses_1a2b3c4d5e6fGarden0001', 1790762405000, 1790762420500,
  '{"role":"assistant","time":{"created":1790762405000,"completed":1790762420500},"parentID":"msg_01","modelID":"gpt-5.6-sol","providerID":"openai","mode":"build","agent":"build","path":{"cwd":"/work/garden","root":"/work/garden"},"cost":0,"tokens":{"total":1700,"input":1000,"output":200,"reasoning":100,"cache":{"read":400,"write":0}},"variant":"high","finish":"tool-calls"}');
INSERT INTO part VALUES ('prt_02a', 'msg_02', 'ses_1a2b3c4d5e6fGarden0001', 1790762405000, 1790762405000,
  '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_02b', 'msg_02', 'ses_1a2b3c4d5e6fGarden0001', 1790762405500, 1790762405500,
  '{"type":"reasoning","text":"Which beds need work?","time":{"start":1790762405500,"end":1790762405900}}');
INSERT INTO part VALUES ('prt_02c', 'msg_02', 'ses_1a2b3c4d5e6fGarden0001', 1790762406000, 1790762407000,
  '{"type":"tool","callID":"call_1","tool":"bash","state":{"status":"completed","input":{"command":"ls /work/garden/beds","description":"List beds"},"output":"rose\ntulip\n","title":"List beds","metadata":{},"time":{"start":1790762406000,"end":1790762407000}}}');
INSERT INTO part VALUES ('prt_02d', 'msg_02', 'ses_1a2b3c4d5e6fGarden0001', 1790762407000, 1790762407100,
  '{"type":"tool","callID":"call_2","tool":"read","state":{"status":"error","input":{"filePath":"/work/garden/missing.txt"},"error":"File not found: /work/garden/missing.txt","time":{"start":1790762407000,"end":1790762407100}}}');
INSERT INTO part VALUES ('prt_02e', 'msg_02', 'ses_1a2b3c4d5e6fGarden0001', 1790762408000, 1790762420000,
  '{"type":"tool","callID":"call_3","tool":"task","state":{"status":"completed","input":{"description":"Survey the beds","prompt":"Survey every bed","subagent_type":"explore"},"output":"Two beds surveyed.","title":"Survey the beds","metadata":{"sessionId":"ses_9z8y7x6w5v4uSurvey0002"},"time":{"start":1790762408000,"end":1790762420000}}}');
INSERT INTO part VALUES ('prt_02f', 'msg_02', 'ses_1a2b3c4d5e6fGarden0001', 1790762420500, 1790762420500,
  '{"type":"step-finish","reason":"tool-calls","cost":0,"tokens":{"input":1000,"output":200,"reasoning":100,"cache":{"read":400,"write":0}}}');

INSERT INTO message VALUES ('msg_03', 'ses_1a2b3c4d5e6fGarden0001', 1790762421000, 1790762423500,
  '{"role":"assistant","time":{"created":1790762421000,"completed":1790762423500},"parentID":"msg_01","modelID":"gpt-5.6-sol","providerID":"openai","mode":"build","agent":"build","path":{"cwd":"/work/garden","root":"/work/garden"},"cost":0,"tokens":{"input":300,"output":50,"reasoning":0,"cache":{"read":1500,"write":0}},"variant":"high","finish":"stop"}');
INSERT INTO part VALUES ('prt_03a', 'msg_03', 'ses_1a2b3c4d5e6fGarden0001', 1790762421000, 1790762421000,
  '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_03b', 'msg_03', 'ses_1a2b3c4d5e6fGarden0001', 1790762421500, 1790762421500,
  '{"type":"text","text":"Tidying now."}');
INSERT INTO part VALUES ('prt_03c', 'msg_03', 'ses_1a2b3c4d5e6fGarden0001', 1790762422000, 1790762422500,
  '{"type":"tool","callID":"call_4","tool":"edit","state":{"status":"completed","input":{"filePath":"/work/garden/beds/rose","oldString":"a\nb","newString":"c"},"output":"Edit applied.","title":"beds/rose","metadata":{},"time":{"start":1790762422000,"end":1790762422500}}}');
INSERT INTO part VALUES ('prt_03d', 'msg_03', 'ses_1a2b3c4d5e6fGarden0001', 1790762423000, 1790762423000,
  '{"type":"text","text":"Both beds are tidy; rose was edited."}');
INSERT INTO part VALUES ('prt_03e', 'msg_03', 'ses_1a2b3c4d5e6fGarden0001', 1790762423500, 1790762423500,
  '{"type":"step-finish","reason":"stop","cost":0,"tokens":{"input":300,"output":50,"reasoning":0,"cache":{"read":1500,"write":0}}}');

INSERT INTO message VALUES ('msg_04', 'ses_1a2b3c4d5e6fGarden0001', 1790762460000, 1790762460000,
  '{"role":"user","time":{"created":1790762460000},"agent":"build","model":{"providerID":"openai","modelID":"gpt-5.6-sol"}}');
INSERT INTO part VALUES ('prt_04a', 'msg_04', 'ses_1a2b3c4d5e6fGarden0001', 1790762460000, 1790762460000,
  '{"type":"text","text":"thanks, one more: compact please"}');
INSERT INTO part VALUES ('prt_04b', 'msg_04', 'ses_1a2b3c4d5e6fGarden0001', 1790762460000, 1790762460000,
  '{"type":"compaction","auto":false}');

INSERT INTO message VALUES ('msg_05', 'ses_1a2b3c4d5e6fGarden0001', 1790762465000, 1790762467000,
  '{"role":"assistant","time":{"created":1790762465000},"parentID":"msg_04","modelID":"gpt-5.6-sol","providerID":"openai","mode":"build","agent":"build","path":{"cwd":"/work/garden","root":"/work/garden"},"cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"error":{"name":"APIError","data":{"message":"rate limited"}}}');
INSERT INTO part VALUES ('prt_05a', 'msg_05', 'ses_1a2b3c4d5e6fGarden0001', 1790762466000, 1790762466000,
  '{"type":"tool","callID":"call_5","tool":"webfetch","state":{"status":"running","input":{"url":"https://example.invalid/guide"},"time":{"start":1790762466000}}}');
INSERT INTO part VALUES ('prt_05b', 'msg_05', 'ses_1a2b3c4d5e6fGarden0001', 1790762467000, 1790762467000,
  '{"type":"mystery","note":"a part type this reader does not know"}');

INSERT INTO message VALUES ('msg_c1', 'ses_9z8y7x6w5v4uSurvey0002', 1790762408000, 1790762408000,
  '{"role":"user","time":{"created":1790762408000},"agent":"explore","model":{"providerID":"anthropic","modelID":"claude-sonnet-4-6"}}');
INSERT INTO part VALUES ('prt_c1a', 'msg_c1', 'ses_9z8y7x6w5v4uSurvey0002', 1790762408000, 1790762408000,
  '{"type":"text","text":"Survey the beds"}');
INSERT INTO message VALUES ('msg_c2', 'ses_9z8y7x6w5v4uSurvey0002', 1790762409000, 1790762410000,
  '{"role":"assistant","time":{"created":1790762409000,"completed":1790762410000},"parentID":"msg_c1","modelID":"claude-sonnet-4-6","providerID":"anthropic","mode":"explore","agent":"explore","path":{"cwd":"/work/garden","root":"/work/garden"},"cost":0.0123,"tokens":{"input":20,"output":30,"reasoning":0,"cache":{"read":1000,"write":200}},"finish":"stop"}');
INSERT INTO part VALUES ('prt_c2a', 'msg_c2', 'ses_9z8y7x6w5v4uSurvey0002', 1790762409500, 1790762410000,
  '{"type":"tool","callID":"call_c1","tool":"glob","state":{"status":"completed","input":{"pattern":"*","path":"/work/garden/beds"},"output":"rose\ntulip","title":"beds","metadata":{},"time":{"start":1790762409500,"end":1790762410000}}}');
INSERT INTO part VALUES ('prt_c2b', 'msg_c2', 'ses_9z8y7x6w5v4uSurvey0002', 1790762410000, 1790762410000,
  '{"type":"text","text":"Two beds surveyed."}');

INSERT INTO message VALUES ('msg_t1', 'ses_twin0000000000000000A1', 1790766000000, 1790766000000,
  '{"role":"user","time":{"created":1790766000000},"agent":"build","model":{"providerID":"openai","modelID":"gpt-5.6-sol"}}');
INSERT INTO part VALUES ('prt_t1a', 'msg_t1', 'ses_twin0000000000000000A1', 1790766000000, 1790766000000,
  '{"type":"text","text":"first twin"}');
INSERT INTO message VALUES ('msg_t2', 'ses_twin0000000000000000B2', 1790769600000, 1790769600000,
  '{"role":"user","time":{"created":1790769600000},"agent":"build","model":{"providerID":"openai","modelID":"gpt-5.6-sol"}}');
INSERT INTO part VALUES ('prt_t2a', 'msg_t2', 'ses_twin0000000000000000B2', 1790769600000, 1790769600000,
  '{"type":"text","text":"second twin"}');
