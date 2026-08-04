// Loose typing for i18next keys: any string is accepted. Keeping keys loose
// lets translation files grow without the type layer breaking builds.
import "i18next";

declare module "i18next" {
  interface CustomTypeOptions {
    defaultNS: "translation";
    resources: {
      translation: Record<string, never>;
    };
  }
}
