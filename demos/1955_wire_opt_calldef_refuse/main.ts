class C {
  n: number = 0;
  constructor(t: string|null) { this.n = t?.length ?? bump(); }
}
function bump(): number { return 42; }
function main(): i32 {
  const c = new C("hey");
  console.log(c.n);
  return 0;
}
