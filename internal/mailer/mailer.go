package mailer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/mailer/transport"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/jordan-wright/email"
	"github.com/rs/zerolog/log"
)

var (
	ErrEmailTemplateNotFound          = errors.New("email template not found")
	emailTemplatePasswordReset        = "password_reset"        // /app/templates/email/password_reset/**.
	emailTemplateAccountConfirmation  = "account_confirmation"  // /app/templates/email/account_confirmation/**
	emailTemplateMembershipInvitation = "membership_invitation" // /app/templates/email/membership_invitation/**
	emailTemplateAccountExists        = "account_exists"        // /app/templates/email/account_exists/**
)

type Mailer struct {
	Config    config.Mailer
	Transport transport.MailTransporter
	Templates map[string]*template.Template
}

func New(config config.Mailer, transport transport.MailTransporter) *Mailer {
	return &Mailer{
		Config:    config,
		Transport: transport,
		Templates: map[string]*template.Template{},
	}
}

func NewWithConfig(cfg config.Mailer, smtpConfig transport.SMTPMailTransportConfig) (*Mailer, error) {
	var mailer *Mailer

	switch config.MailerTransporter(cfg.Transporter) {
	case config.MailerTransporterMock:
		log.Warn().Msg("Initializing mock mailer")
		mailer = New(cfg, transport.NewMock())
	case config.MailerTransporterSMTP:
		mailer = New(cfg, transport.NewSMTP(smtpConfig))
	default:
		return nil, fmt.Errorf("unsupported mail transporter: %s", cfg.Transporter)
	}

	if err := mailer.ParseTemplates(); err != nil {
		return nil, fmt.Errorf("failed to parse mailer templates: %w", err)
	}

	return mailer, nil
}

func (m *Mailer) ParseTemplates() error {
	files, err := os.ReadDir(m.Config.WebTemplatesEmailBaseDirAbs)
	if err != nil {
		log.Error().Str("dir", m.Config.WebTemplatesEmailBaseDirAbs).Err(err).Msg("Failed to read email templates directory while parsing templates")
		return fmt.Errorf("failed to read email templates directory while parsing templates: %w", err)
	}

	for _, file := range files {
		if !file.IsDir() {
			continue
		}

		tmpl, err := template.ParseGlob(filepath.Join(m.Config.WebTemplatesEmailBaseDirAbs, file.Name(), "**"))
		if err != nil {
			log.Error().Str("template", file.Name()).Err(err).Msg("Failed to parse email template files as glob")
			return fmt.Errorf("failed to parse email template files as glob: %w", err)
		}

		m.Templates[file.Name()] = tmpl
	}

	return nil
}

func (m *Mailer) SendPasswordReset(ctx context.Context, to string, passwordResetLink string) error {
	log := util.LogFromContext(ctx).With().Str("component", "mailer").Str("email_template", emailTemplatePasswordReset).Logger()

	tmpl, ok := m.Templates[emailTemplatePasswordReset]
	if !ok {
		log.Error().Msg("Password reset email template not found")
		return ErrEmailTemplateNotFound
	}

	data := map[string]interface{}{
		"passwordResetLink": passwordResetLink,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		log.Error().Err(err).Msg("Failed to execute password reset email template")
		return fmt.Errorf("failed to execute password reset email template: %w", err)
	}

	mail := email.NewEmail()

	mail.From = m.Config.DefaultSender
	mail.To = []string{to}
	mail.Subject = "Password reset"
	mail.HTML = buf.Bytes()

	if !m.Config.Send {
		log.Warn().Str("to", to).Str("passwordResetLink", passwordResetLink).Msg("Sending has been disabled in mailer config, skipping password reset email")
		return nil
	}

	if err := m.Transport.Send(mail); err != nil {
		log.Debug().Err(err).Msg("Failed to send password reset email")
		return fmt.Errorf("failed to send password reset email: %w", err)
	}

	log.Debug().Msg("Successfully sent password reset email")

	return nil
}

func (m *Mailer) SendAccountConfirmation(ctx context.Context, to string, payload dto.ConfirmatioNotificationPayload) error {
	log := util.LogFromContext(ctx)

	tmpl, ok := m.Templates[emailTemplateAccountConfirmation]
	if !ok {
		log.Error().Msg("Account confirmation email template not found")
		return ErrEmailTemplateNotFound
	}

	data := map[string]interface{}{
		"confirmationLink": payload.ConfirmationLink,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		log.Error().Err(err).Msg("Failed to execute account confirmation email template")
		return fmt.Errorf("failed to execute account confirmation email template: %w", err)
	}

	mail := email.NewEmail()

	mail.From = m.Config.DefaultSender
	mail.To = []string{to}
	mail.Subject = "Account confirmation"
	mail.HTML = buf.Bytes()

	if !m.Config.Send {
		log.Warn().Str("to", to).Msg("Sending has been disabled in mailer config, skipping account confirmation email")
		return nil
	}

	if err := m.Transport.Send(mail); err != nil {
		log.Debug().Err(err).Msg("Failed to send account confirmation email")
		return fmt.Errorf("failed to send account confirmation email: %w", err)
	}

	return nil
}

// SendMembershipInvitation mails an approved applicant their invitation link.
//
// invitationLink carries the only copy of the plaintext token -- the database
// holds just its hash -- so a failure here means the invitation is unreachable
// and must be re-issued rather than recovered.
//
// The template renders {{ .fullName }} through html/template, which escapes it.
// That matters: fullName is attacker-supplied, arrives from an unauthenticated
// endpoint, and is rendered into HTML that lands in a human's inbox. Never
// switch these fields to a raw-HTML pipeline to make formatting work.
func (m *Mailer) SendMembershipInvitation(ctx context.Context, to string, fullName string, invitationLink string, expiresAt time.Time) error {
	log := util.LogFromContext(ctx).With().
		Str("component", "mailer").
		Str("email_template", emailTemplateMembershipInvitation).
		Logger()

	tmpl, ok := m.Templates[emailTemplateMembershipInvitation]
	if !ok {
		log.Error().Msg("Membership invitation email template not found")

		return ErrEmailTemplateNotFound
	}

	data := map[string]interface{}{
		"fullName":       fullName,
		"invitationLink": invitationLink,
		"expiresAt":      expiresAt.Format("2 January 2006"),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		log.Error().Err(err).Msg("Failed to execute membership invitation email template")

		return fmt.Errorf("failed to execute membership invitation email template: %w", err)
	}

	mail := email.NewEmail()
	mail.From = m.Config.DefaultSender
	mail.To = []string{to}
	mail.Subject = "Your Cloudadore invitation"
	mail.HTML = buf.Bytes()

	if !m.Config.Send {
		// Deliberately NOT logging the link: unlike the password-reset path, which
		// does log it for local development, an invitation link creates an account
		// on a site that is otherwise approval-gated.
		log.Warn().Str("to", to).Msg("Sending has been disabled in mailer config, skipping membership invitation email")

		return nil
	}

	if err := m.Transport.Send(mail); err != nil {
		log.Debug().Err(err).Msg("Failed to send membership invitation email")

		return fmt.Errorf("failed to send membership invitation email: %w", err)
	}

	log.Debug().Msg("Successfully sent membership invitation email")

	return nil
}

// SendAccountExists tells someone that a registration attempt hit an address
// that already has an account.
//
// This is the other half of making /register non-disclosing (S-020): the HTTP
// response for an existing address is identical to a fresh registration, so the
// only place the difference surfaces is this email -- which reaches the mailbox
// owner rather than the person making the request.
func (m *Mailer) SendAccountExists(ctx context.Context, to string) error {
	log := util.LogFromContext(ctx).With().
		Str("component", "mailer").
		Str("email_template", emailTemplateAccountExists).
		Logger()

	tmpl, ok := m.Templates[emailTemplateAccountExists]
	if !ok {
		log.Error().Msg("Account exists email template not found")

		return ErrEmailTemplateNotFound
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]interface{}{}); err != nil {
		log.Error().Err(err).Msg("Failed to execute account exists email template")

		return fmt.Errorf("failed to execute account exists email template: %w", err)
	}

	mail := email.NewEmail()
	mail.From = m.Config.DefaultSender
	mail.To = []string{to}
	mail.Subject = "You already have a Cloudadore account"
	mail.HTML = buf.Bytes()

	if !m.Config.Send {
		log.Warn().Str("to", to).Msg("Sending has been disabled in mailer config, skipping account exists email")

		return nil
	}

	if err := m.Transport.Send(mail); err != nil {
		log.Debug().Err(err).Msg("Failed to send account exists email")

		return fmt.Errorf("failed to send account exists email: %w", err)
	}

	return nil
}
