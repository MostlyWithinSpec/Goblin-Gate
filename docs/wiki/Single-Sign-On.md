# Single Sign-On

Log in to the web UI with the identity provider you already run, such as Authentik, Keycloak, Authelia,
Microsoft Entra ID or Google, instead of a separate password for the gate. The login page then shows a
**Sign in with …** button next to the usual form. It uses OpenID Connect (the authorization code flow with
PKCE).

- **Local accounts keep working.** Keep at least one local admin: it is your way in if the provider is down
  or misconfigured.
- **Nobody gets an account on the gate.** A user exists only while the provider vouches for them. Their role
  comes from their groups each time they sign in.
- **Two-factor login is the provider's job** for these users. Turn it on there.

## Set up the provider

Create an OpenID Connect client (Authentik calls it a provider plus an application; Keycloak and Entra ID
call it a client or an app registration):

| Setting | Value |
| --- | --- |
| Client type | Confidential (with a secret). A public client without a secret works too, as PKCE protects the flow. |
| Redirect URL | `https://<your gate>/api/v1/auth/sso/callback`, using the address you open the web UI with |
| Scopes | `openid`, `profile`, `email`, and a groups claim (see below) |

The gate needs to know each user's groups:

- **Authentik**: the `groups` claim is in the default `profile` scope mapping.
- **Keycloak**: add a mapper to the client: *Client scopes → gate-dedicated → Add mapper → Group Membership*,
  token claim name `groups`, *Full group path* off.
- **Authelia**: add `groups` to the client's scopes.
- **Entra ID**: *Token configuration → Add groups claim*. Entra puts group IDs in the claim, so map IDs, not
  names.

## Set up the gate

**System → Admins → Single sign-on** (or the `sso` section of the config):

| Setting | What it does |
| --- | --- |
| Name | The text on the button: *Sign in with Authentik* |
| Issuer | The provider's issuer URL, e.g. `https://auth.example.com/application/o/gate/` (Authentik) or `https://kc.example.com/realms/home` (Keycloak). The gate reads `<issuer>/.well-known/openid-configuration`. |
| Client ID, Client secret | From the provider |
| Group → role | Which group gets which role: `admin`, `readonly`, or a custom role (see [admin roles](Remote-Access-SSH-API-CLI#admin-roles)). The first matching line wins. |
| Default role | The role for users in none of those groups. Leave it empty to refuse them. |
| Username claim | Which claim names the user. The default is `preferred_username`, then `email`. |
| Groups claim | The default is `groups`. |
| Scopes | Leave empty for `profile`, `email` and `groups`, as far as the provider offers them. |
| Redirect URL | Only if the gate is reached under a name the browser doesn't use (behind a proxy). |
| CA certificate | A provider with a certificate from a private CA: paste that CA's certificate. For a provider with a certificate from the gate's own [[Internal CA]], paste the gate's root. |

Commit, then log out and use the new button.

## When sign-in fails

The login page says why. The audit log has the details (`login_failed` with `via: sso` and a reason):

| Reason | What happened |
| --- | --- |
| `no_role` | The user is in none of the mapped groups and there is no default role. Check the groups claim: the audit entry lists the groups the provider sent. |
| `local_user` | A local account has the same name. The provider may never speak for a local account, so rename one of them. |
| `no_username` | The username claim is missing or has odd characters (allowed: letters, digits, `.` `_` `@` `+` `-`). |
| `refused` | The provider said no: the user cancelled, or isn't allowed to use this client. |
| `provider` | The gate couldn't reach the provider or read its configuration. Check the issuer URL, DNS, and the CA certificate. |
| `exchange`, `token` | The provider's answer didn't check out: a wrong client secret or redirect URL, or the clocks of the gate and the provider are more than a minute apart. |
| `state` | The sign-in took longer than 10 minutes, or was started in another browser. Try again. |

Turning single sign-on off ends every session that came from it at once. Changing a role's scopes takes
effect immediately. A role that a group maps to can't be deleted until the mapping goes.
