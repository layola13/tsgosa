class MinHeap {
  a: number[];
  constructor(a: number[]) { this.a = a; }
  push(v: number): void {
    this.a.push(v);
    const a = this.a;
    let i = a.length - 1;
    while (i > 0) {
      const p = (i - 1) / 2;
      if (a[p] <= a[i]) { break; }
      const t = a[p]; a[p] = a[i]; a[i] = t;
      i = p;
    }
  }
  peek(): number { return this.a[0]; }
  size(): number { return this.n(); }
  n(): number { return this.a.length; }
}
function main(): i32 {
  const h = new MinHeap([]);
  h.push(5); h.push(3); h.push(8); h.push(1);
  expect(h.peek()).toBe(1);
  expect(h.size()).toBe(4);
  console.log(h.peek());
  return 0;
}
