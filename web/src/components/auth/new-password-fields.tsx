import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  PASSWORD_MAX_LENGTH,
  PASSWORD_MIN_LENGTH,
} from "@/lib/password-policy"

type NewPasswordFieldsProps = {
  error: string
  passwordLabel?: string
  confirmationLabel?: string
}

export function NewPasswordFields({
  error,
  passwordLabel = "Create password",
  confirmationLabel = "Confirm password",
}: NewPasswordFieldsProps) {
  return (
    <>
      <Field data-invalid={Boolean(error)}>
        <FieldLabel htmlFor="password">{passwordLabel}</FieldLabel>
        <Input
          id="password"
          name="password"
          type="password"
          minLength={PASSWORD_MIN_LENGTH}
          maxLength={PASSWORD_MAX_LENGTH}
          autoComplete="new-password"
          required
        />
        <FieldDescription>
          Use at least {PASSWORD_MIN_LENGTH} characters.
        </FieldDescription>
      </Field>
      <Field data-invalid={Boolean(error)}>
        <FieldLabel htmlFor="confirmation">{confirmationLabel}</FieldLabel>
        <Input
          id="confirmation"
          name="confirmation"
          type="password"
          minLength={PASSWORD_MIN_LENGTH}
          maxLength={PASSWORD_MAX_LENGTH}
          autoComplete="new-password"
          required
        />
        <FieldError>{error}</FieldError>
      </Field>
    </>
  )
}
