export namespace Mini {
  export type ID = number;
  export interface Str {
    type: "string";
    min_length?: number;
  }
}
export interface Cfg {
  kind: "basic";
  retries: number;
  meta: { version: number };
  "~tag": number;
}
export class Box {
  v: i32 = 0;
  set(x: i32): void;
  set(x: boolean): void;
  set(x: i32): void {
    this.v = x;
  }
}
