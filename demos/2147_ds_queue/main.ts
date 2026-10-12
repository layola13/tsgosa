class Queue {
  a: number[];
  head: number;
  constructor(a: number[], head: number) { this.a = a; this.head = head; }
  enqueue(v: number): void { this.a.push(v); }
  dequeue(): number { const v = this.a[this.head]; this.head = this.head + 1; return v; }
  size(): number { return this.a.length - this.head; }
}
function main(): i32 {
  const q = new Queue([], 0);
  q.enqueue(1); q.enqueue(2); q.enqueue(3);
  expect(q.dequeue()).toBe(1);
  expect(q.size()).toBe(2);
  console.log(q.dequeue());
  return 0;
}
