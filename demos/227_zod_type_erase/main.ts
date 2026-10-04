export namespace Mini {
  export type ID = number;
  export interface Str {
    type: "string";
    min_length?: number;
  }
}
interface Cfg {
  kind: "basic";
  retries: number;
  meta: { version: number };
  "~tag": number;
}
function get(r: number): number {
  return r + 2;
}
function main(): i32 {
  console.log(get(40));
  return 0;
}
