// Typed request/response shapes matching backend/internal/authhttp's exact
// JSON structures (Step 9C). These interfaces mirror the Go structs field
// for field; they are not a redesign of the wire contract.

export interface LoginRequest {
  email: string;
  password: string;
  device_hint?: string;
}

export interface FirstTimeLoginRequest {
  token: string;
  password: string;
}

export interface PasswordResetRequestBody {
  email: string;
}

export interface PasswordResetCompleteBody {
  token: string;
  password: string;
}

export interface MeResponse {
  user_id: number;
  email: string;
  display_name: string;
  role: string;
  scope?: string;
  status: string;
}

export interface StatusResponse {
  status: string;
}

export interface MessageResponse {
  message: string;
}

export interface SessionView {
  id: number;
  device_hint?: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
  current: boolean;
}

export interface ApiErrorBody {
  code: string;
  message: string;
}

export interface ApiErrorEnvelope {
  error: ApiErrorBody;
}
