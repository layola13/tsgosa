class A {
  constructor(x: number);
  constructor(x: any) {
    this.v = x;
  }
  v = 0;
}
function main(): number {
  const a = new A(5);
  return a.v;
}
console.log(main());
