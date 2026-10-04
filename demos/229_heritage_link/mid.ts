import { B } from "./base";
export class C extends B {
  y: i32 = 0;
  constructor(x: i32, y: i32) {
    super(x);
    this.y = y;
  }
}
