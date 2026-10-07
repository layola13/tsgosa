class C {
  [Symbol.iterator](): any {
    return null;
  }
  #hidden(): i32 { return 3; }
  pub(): i32 { return 4; }
}
function main(): i32 {
  const c = new C();
  console.log(c.pub());
  return c.pub();
}
console.log(main());
