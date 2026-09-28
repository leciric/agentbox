// Stylesheets are imported for their side effects; Vite bundles them.
declare module '*.css';

// An asset imported with ?url is its URL in the build; Vite copies the file.
declare module '*?url' {
  const url: string;
  export default url;
}
