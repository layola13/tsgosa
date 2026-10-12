class C {
  t: string = "hi";
  s: string = "";
  constructor() { this.s = this.t + "!"; }
}
function main(): i32 {
  const c = new C();
  console.log(c.s);
  return 0;
}
