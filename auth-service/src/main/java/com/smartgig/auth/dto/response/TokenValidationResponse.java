package com.smartgig.auth.dto.response;

public class TokenValidationResponse {
    private boolean valid;
    private Long userId;
    private String username;
    private String role;

    public TokenValidationResponse() {}

    public TokenValidationResponse(boolean valid, Long userId, String username, String role) {
        this.valid = valid;
        this.userId = userId;
        this.username = username;
        this.role = role;
    }

    public boolean isValid() { return valid; }
    public void setValid(boolean valid) { this.valid = valid; }
    public Long getUserId() { return userId; }
    public void setUserId(Long userId) { this.userId = userId; }
    public String getUsername() { return username; }
    public void setUsername(String username) { this.username = username; }
    public String getRole() { return role; }
    public void setRole(String role) { this.role = role; }
}