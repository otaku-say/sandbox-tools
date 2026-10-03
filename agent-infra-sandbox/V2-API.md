# aiod v2 API 速查（autogen from v2/openapi.json, 0.9.2）

路径 65 条。响应统一信封：`{success,message,data,hint}`；**data 才是业务体**。

- GET    /health
- POST   /mcp
    · Model Context Protocol endpoint
- POST   /v2/browser/cdp  body=CdpRequest{browser:boolean, method*:string, params:?, tab_id:string|null}
- POST   /v2/browser/click  body=ClickRequest{ref:string|null, selector:string|null, tab_id:string|null}
- POST   /v2/browser/config  body=BrowserConfigRequest{resolution:?}
- DELETE /v2/browser/cookies  params=name*, url*, domain*, all*
- GET    /v2/browser/cookies  params=url*, domain*
- POST   /v2/browser/cookies  body=SetCookiesRequest{cookies*:array}
- POST   /v2/browser/evaluate  body=EvaluateRequest{await_promise:boolean, expression*:string, tab_id:string|null}
- POST   /v2/browser/fill  body=FillRequest{ref:string|null, selector:string|null, tab_id:string|null, value*:string}
- GET    /v2/browser/info
- POST   /v2/browser/navigate  body=NavigateRequest{history:string|null, tab_id:string|null, timeout:number, url:string|null, wait_until:WaitUntil}
    · Navigate the active page (or `tab_id`) and wait for `wait_until` — `lo
- GET    /v2/browser/network/requests  params=limit*, clear*
- GET    /v2/browser/screenshot  params=format, full_page, quality
- POST   /v2/browser/snapshot  body=SnapshotRequest{interactive_only:boolean, tab_id:string|null}
- GET    /v2/browser/tabs
- POST   /v2/browser/tabs  body=NewTabRequest{url:string|null}
- DELETE /v2/browser/tabs/{tab_id}  params=tab_id*
- POST   /v2/browser/tabs/{tab_id}/activate  params=tab_id*
- POST   /v2/browser/upload  body=UploadRequest{paths*:array, ref:string|null, selector:string|null, tab_id:string|null}
- POST   /v2/code/execute
    · Run code (language-dispatched)
- GET    /v2/code/info
    · Supported languages and backend info
- GET    /v2/code/sessions
    · List code sessions
- POST   /v2/code/sessions
    · Create a code session
- DELETE /v2/code/sessions/{session_id}
    · Delete a code session
- GET    /v2/code/sessions/{session_id}
    · Get a code session
- POST   /v2/commands  body=CommandRunRequest{args:array|null, command*:string, cwd:string|null, env:object|null, hard_timeout:number|null, max_output_length:integer, mode:RunMode, session:string|null, shell:ShellSelector, timeout:number|null, user:string|null}
- GET    /v2/commands/sessions
- POST   /v2/commands/sessions  body=CommandSessionCreateRequest{cwd:string|null, env:object|null, id:string|null, user:string|null}
- DELETE /v2/commands/sessions/{session_id}  params=session_id*
- GET    /v2/commands/{command_id}  params=command_id*, offset, stderr_offset, wait, wait_timeout
- POST   /v2/commands/{command_id}/kill  body=CommandKillBody{signal:string}
- POST   /v2/commands/{command_id}/stdin  body=CommandStdinBody{input*:string}
- GET    /v2/computer/accessibility  params=scope*, max_depth*, max_nodes*, role*, name*, match*, states*, include_offscreen*, timeout_ms*
- GET    /v2/computer/accessibility/nodes  params=scope*, max_depth*, max_nodes*, role*, name*, match*, states*, include_offscreen*, timeout_ms*, limit*, node_id*
- POST   /v2/computer/actions  params=include_screenshot  body=DisplayAction{}
- POST   /v2/computer/actions/batch  body=DisplayActionBatchRequest{actions*:array, include_screenshot:boolean}
- GET    /v2/computer/clipboard
- GET    /v2/computer/cursor
- GET    /v2/computer/info
- POST   /v2/computer/record  body=DisplayRecordRequest{action:string, crf:integer, fps:integer, height:integer|null, max_duration:number, save_path:string|null, width:integer|null}
- GET    /v2/computer/screenshot
- GET    /v2/computer/windows
- POST   /v2/fs/copy  params=user  body=FileCopyRequest{destination*:string, overwrite:boolean, source*:string}
- POST   /v2/fs/delete  params=user  body=FileDeleteRequest{path*:string, recursive:boolean}
- GET    /v2/fs/download  params=path*
- POST   /v2/fs/edit  params=user  body=FsEditRequest{command*:string, insert_line:integer|null, new_str:string|null, old_str:string|null, path*:string, replace_mode:string|null}
- POST   /v2/fs/grep  params=user  body=FileGrepRequest{case_insensitive:boolean, context_after:integer, context_before:integer, exclude:array|null, fixed_strings:boolean, include:array|null, max_file_size:string|null, max_results:integer, multiline:boolean, offset:integer, path*:string, pattern*:string, recursive:boolean, type:string|null}
- GET    /v2/fs/list  params=user, path*, recursive, show_hidden, max_depth
- POST   /v2/fs/mkdir  params=user  body=FileMkdirRequest{parents:boolean, path*:string}
- POST   /v2/fs/move  params=user  body=FileMoveRequest{destination*:string, overwrite:boolean, source*:string}
- GET    /v2/fs/read  params=user, path*, start_line, end_line
- GET    /v2/fs/search  params=user, path*, pattern*
- GET    /v2/fs/stat  params=user, path*, follow_symlinks
- GET    /v2/fs/tree  params=path*
- PUT    /v2/fs/tree  params=user, path*  body=array
- POST   /v2/fs/upload  params=user
- POST   /v2/fs/write  params=user  body=FsWriteRequest{append:boolean, content*:string, encoding:FileContentEncoding, leading_newline:boolean, path*:string, trailing_newline:boolean}
- GET    /v2/pty/sessions
- POST   /v2/pty/sessions  body=PtyCreateRequest{cols:integer|null, cwd:string|null, env:object|null, id:string|null, no_change_timeout:integer|null, retention:string|null, rows:integer|null, user:string|null}
- DELETE /v2/pty/sessions/{id}  params=id*
- GET    /v2/pty/sessions/{id}  params=id*
- PATCH  /v2/pty/sessions/{id}  params=id*  body=PtyPatchRequest{cols:integer|null, no_change_timeout:integer|null, rows:integer|null}
- POST   /v2/pty/sessions/{id}/exec  params=id*  body=PtyExecRequest{async:boolean, command*:string, hard_timeout:number|null, no_change_timeout:integer|null, timeout:number|null}
- POST   /v2/pty/sessions/{id}/input  params=id*  body=PtyInputRequest{input*:string, press_enter:boolean}
- GET    /v2/pty/sessions/{id}/screen  params=id*
- POST   /v2/pty/sessions/{id}/signal  params=id*
- GET    /v2/pty/sessions/{id}/ws  params=id*, protocol, durable, restore, replay_bytes
- GET    /v2/pty/ws  params=protocol
    · Anonymous ephemeral WebShell (connection-scoped)
- GET    /v2/sandbox
    · Environment self-description in one call
- GET    /v2/sandbox/packages  params=lang*
    · Installed packages for a language runtime
- GET    /v2/watch
- POST   /v2/watch  body=CreateWatchRequest{debounce:integer, exclude:array, include_patterns:array, path*:string, recursive:boolean}
- DELETE /v2/watch/{watcher_id}  params=watcher_id*
- GET    /v2/watch/{watcher_id}/events  params=watcher_id*
- GET    /v2/watch/{watcher_id}/poll  params=watcher_id*, cursor, limit, timeout
