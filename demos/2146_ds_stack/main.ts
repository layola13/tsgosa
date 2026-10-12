class Stack {
  a: number[];
  n: number;
  constructor(a: number[], n: number) { this.a = a; this.n = n; }
  push(v: number): void { this.a.push(v); this.n = this.n + 1; }
  pop(): number { this.n = this.n - 1; return this.a.pop(); }
  size(): number { return this.n; }
}
function main(): i32 {
  const s = new Stack([], 0);
  s.push(10); s.push(20); s.push(30);
  expect(s.pop()).toBe(30);
  expect(s.size()).toBe(2);
  console.log(s.pop() + s.pop());
  return 0;
}
