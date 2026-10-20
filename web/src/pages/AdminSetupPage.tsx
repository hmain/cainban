import { useState } from "react";
import { Link } from "react-router";

/**
 * AdminSetupPage guides operators through the one-time GitHub OAuth App setup.
 * This is NOT for end users — it's for whoever deploys cainban.
 *
 * The flow:
 * 1. Click link to create OAuth App on GitHub (pre-filled where possible)
 * 2. Copy Client ID and Client Secret from GitHub
 * 3. Paste them here to save to AWS Secrets Manager
 * 4. Test the device flow endpoint
 */
export function AdminSetupPage() {
  return (
    <section className="admin-setup-page">
      <div className="account-row">
        <h2>GitHub OAuth Setup (Admin)</h2>
        <Link className="link" to="/">
          ← Back
        </Link>
      </div>

      <div className="setup-intro card">
        <p>
          This is a one-time setup for administrators. You'll create a GitHub
          OAuth App and save its credentials to AWS Secrets Manager.
        </p>
        <p className="hint">
          After setup, users authenticate directly with GitHub using their own
          accounts — no Cognito federation needed.
        </p>
      </div>

      <Step1CreateApp />
      <Step2EnableDeviceFlow />
      <Step3SaveCredentials />
      <Step4Test />
    </section>
  );
}

function Step1CreateApp() {
  // Pre-fill what we can via URL params
  const appName = "cainban";
  const homepageUrl = window.location.origin;
  const callbackUrl = "http://localhost/callback"; // Placeholder, not used for device flow

  // GitHub's OAuth App creation doesn't support URL pre-filling, but we can
  // provide the exact values to copy
  const createUrl = "https://github.com/settings/applications/new";

  return (
    <section className="card step">
      <h3>
        <span className="step-number">1</span> Create GitHub OAuth App
      </h3>

      <p>
        Go to GitHub and create a new OAuth App with these settings:
      </p>

      <a
        href={createUrl}
        target="_blank"
        rel="noopener noreferrer"
        className="btn primary"
      >
        Open GitHub OAuth App Settings →
      </a>

      <table className="settings-table">
        <tbody>
          <tr>
            <td className="label">Application name</td>
            <td>
              <CopyableValue value={appName} />
            </td>
          </tr>
          <tr>
            <td className="label">Homepage URL</td>
            <td>
              <CopyableValue value={homepageUrl} />
            </td>
          </tr>
          <tr>
            <td className="label">Authorization callback URL</td>
            <td>
              <CopyableValue value={callbackUrl} />
              <span className="hint small"> (placeholder, not used)</span>
            </td>
          </tr>
        </tbody>
      </table>

      <p className="hint">
        After creating, you'll see the <strong>Client ID</strong>. Click
        "Generate a new client secret" to get the <strong>Client Secret</strong>.
      </p>
    </section>
  );
}

function Step2EnableDeviceFlow() {
  return (
    <section className="card step">
      <h3>
        <span className="step-number">2</span> Enable Device Flow
      </h3>

      <p>
        <strong>Critical:</strong> Device Flow must be explicitly enabled or the
        auth endpoint won't work.
      </p>

      <ol className="steps-list">
        <li>On your OAuth App's settings page, scroll down</li>
        <li>
          Check <strong>☑ Enable Device Flow</strong>
        </li>
        <li>Click "Update application"</li>
      </ol>

      <div className="warning-box">
        <strong>⚠️ Without this step</strong>, the{" "}
        <code>/auth/github/device</code> endpoint returns:
        <pre className="error-preview">
          "Device Flow must be explicitly enabled for this App"
        </pre>
      </div>
    </section>
  );
}

function Step3SaveCredentials() {
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [region, setRegion] = useState("eu-north-1");
  const [copied, setCopied] = useState(false);

  const secretJson = JSON.stringify(
    {
      client_id: clientId || "YOUR_CLIENT_ID",
      client_secret: clientSecret || "YOUR_CLIENT_SECRET",
    },
    null,
    2
  );

  const awsCommand = `aws secretsmanager put-secret-value \\
  --secret-id cainban/github-oauth \\
  --secret-string '${secretJson.replace(/\n/g, "").replace(/'/g, "\\'")}' \\
  --region ${region}`;

  const copyCommand = async () => {
    await navigator.clipboard.writeText(awsCommand);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <section className="card step">
      <h3>
        <span className="step-number">3</span> Save Credentials to AWS
      </h3>

      <p>Paste the credentials from GitHub:</p>

      <div className="form-grid">
        <label>
          Client ID
          <input
            type="text"
            value={clientId}
            onChange={(e) => setClientId(e.target.value)}
            placeholder="Iv23liXXXXXXXXXXXXXX"
            className="input"
          />
        </label>

        <label>
          Client Secret
          <input
            type="password"
            value={clientSecret}
            onChange={(e) => setClientSecret(e.target.value)}
            placeholder="••••••••••••••••••••"
            className="input"
          />
        </label>

        <label>
          AWS Region
          <input
            type="text"
            value={region}
            onChange={(e) => setRegion(e.target.value)}
            className="input"
          />
        </label>
      </div>

      <p className="hint">
        Run this command to save the credentials to Secrets Manager:
      </p>

      <div className="command-box">
        <pre className="command">{awsCommand}</pre>
        <button
          className="copy-btn"
          onClick={() => void copyCommand()}
          aria-label="Copy command"
        >
          {copied ? "✓ Copied" : "Copy"}
        </button>
      </div>

      <p className="hint small">
        Make sure the secret <code>cainban/github-oauth</code> exists first. If
        not, create it with <code>aws secretsmanager create-secret</code>.
      </p>
    </section>
  );
}

function Step4Test() {
  const [testing, setTesting] = useState(false);
  const [result, setResult] = useState<{
    ok: boolean;
    message: string;
    data?: Record<string, unknown>;
  } | null>(null);

  const testEndpoint = async () => {
    setTesting(true);
    setResult(null);

    try {
      // Get the MCP API URL from the well-known endpoint or environment
      const baseUrl =
        import.meta.env.VITE_MCP_API_URL ||
        window.location.origin.replace(/:\d+$/, "") + ":443";

      const resp = await fetch(`${baseUrl}/auth/github/device`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
      });

      const data = await resp.json();

      if (data.user_code && data.verification_uri) {
        setResult({
          ok: true,
          message: `✅ Device flow working! User code: ${data.user_code}`,
          data,
        });
      } else if (data.error) {
        setResult({
          ok: false,
          message: `❌ Error: ${data.error} - ${data.error_description || ""}`,
          data,
        });
      } else {
        setResult({
          ok: false,
          message: "Unexpected response",
          data,
        });
      }
    } catch (e) {
      setResult({
        ok: false,
        message: `❌ Failed to connect: ${e}`,
      });
    } finally {
      setTesting(false);
    }
  };

  return (
    <section className="card step">
      <h3>
        <span className="step-number">4</span> Test the Setup
      </h3>

      <p>
        After saving credentials and redeploying the Lambda, test the device
        flow:
      </p>

      <button
        className="btn primary"
        onClick={() => void testEndpoint()}
        disabled={testing}
      >
        {testing ? "Testing..." : "Test Device Flow Endpoint"}
      </button>

      {result && (
        <div className={`result-box ${result.ok ? "success" : "error"}`}>
          <p>{result.message}</p>
          {result.data && (
            <details>
              <summary>Response details</summary>
              <pre>{JSON.stringify(result.data, null, 2)}</pre>
            </details>
          )}
        </div>
      )}

      <div className="next-steps">
        <h4>What's next?</h4>
        <ul>
          <li>
            Users can now authenticate via{" "}
            <code>POST /auth/github/device</code>
          </li>
          <li>
            CLI clients call the endpoint, show the user code, and poll for
            completion
          </li>
          <li>
            After auth, they use <code>Authorization: Bearer &lt;github_token&gt;</code>{" "}
            for all MCP requests
          </li>
        </ul>
      </div>
    </section>
  );
}

function CopyableValue({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    await navigator.clipboard.writeText(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <span className="copyable-value">
      <code>{value}</code>
      <button className="copy-inline" onClick={() => void copy()}>
        {copied ? "✓" : "📋"}
      </button>
    </span>
  );
}

export default AdminSetupPage;
