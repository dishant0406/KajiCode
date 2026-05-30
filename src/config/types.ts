export interface ProviderProfile {
  name: string;
  baseURL: string;
  apiKey?: string;
  model: string;
  description?: string;
}

export interface ZeroConfig {
  activeProvider?: string;
  providers: ProviderProfile[];
}
