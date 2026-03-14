// Package junie provides authentication and token management functionality
// for JetBrains Junie AI services. It handles JWT and OAuth token storage,
// serialization, and retrieval for maintaining authenticated sessions with
// the Grazie API and JetBrains Account OAuth.
package junie

// LoginSuccessHtml is the HTML page shown to users after a successful JetBrains Account OAuth login.
// It informs the user that authentication was successful and prompts them to close the browser tab.
const LoginSuccessHtml = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>JetBrains Login Successful</title>
    <style>
        * { box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            display: flex;
            justify-content: center;
            align-items: center;
            min-height: 100vh;
            margin: 0;
            background: linear-gradient(135deg, #0a7fc2 0%, #1a237e 100%);
            padding: 1rem;
        }
        .container {
            text-align: center;
            background: white;
            padding: 2.5rem;
            border-radius: 12px;
            box-shadow: 0 10px 25px rgba(0,0,0,0.15);
            max-width: 440px;
            width: 100%;
        }
        .logo {
            font-size: 3rem;
            margin-bottom: 1rem;
        }
        h1 {
            color: #1f2937;
            margin-bottom: 0.75rem;
            font-size: 1.6rem;
            font-weight: 600;
        }
        p {
            color: #6b7280;
            font-size: 1rem;
            line-height: 1.5;
            margin-bottom: 1.5rem;
        }
        .close-btn {
            background: #0a7fc2;
            color: white;
            border: none;
            padding: 0.75rem 2rem;
            border-radius: 8px;
            font-size: 0.95rem;
            cursor: pointer;
            font-weight: 500;
        }
        .close-btn:hover { background: #0969a8; }
    </style>
</head>
<body>
    <div class="container">
        <div class="logo">✓</div>
        <h1>JetBrains login successful!</h1>
        <p>You have successfully authenticated with your JetBrains Account. You can close this tab and return to your terminal.</p>
        <button class="close-btn" onclick="window.close()">Close this tab</button>
    </div>
    <script>
        // Auto-close after 10 seconds
        setTimeout(function() { window.close(); }, 10000);
    </script>
</body>
</html>`
