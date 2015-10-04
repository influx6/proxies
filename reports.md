#Config Reports
This contains a detailed set of internal report style being used in the current library, they are not set standards but provide an overview of the patterns

##Types
  - Session-New:
  This is used when a report exists to report the making of a new session data from a net.Conn from a tcp Accept
    tag: "session-new"
    type: map[string]interface{}
    Format:
      ```
      {
  			"ip",
  			"container",
  			"hostname",
  			"container_ip",
  			"remote_ip",
  			"local_ip",
      }
      ```
      Example:
      ```
      {
  			"ip":           rip,
  			"container":    puid,
  			"hostname":     host,
  			"container_ip": cip,
  			"remote_ip":    con.RemoteAddr().String(),
  			"local_ip":     con.LocalAddr().String(),
      }
      ```
  - SSH_AUTH:
    This is used when a reporter exists in the ssh.StreamServer and reports the format for every authentication process
			tag: "ssh-auth"
      type: map[string]interface{}
      Format:
        ```{
            "user",
            "pass",
            "local_ip",
            "remote_ip",
            "client_version",
            "server_version",
            "session_id",
            "state",
            "protocol",
            "error",
  			}```
      Example:
        ```{
            "user":           meta.User(),
            "pass":           string(pass),
            "local_ip":       meta.LocalAddr().String(),
            "remote_ip":      meta.RemoteAddr().String(),
            "client_version": string(meta.ClientVersion()),
            "server_version": string(meta.ServerVersion()),
            "session_id":     string(meta.SessionID()),
            "state":          state,
            "error":          err.Error(),
  			}```
