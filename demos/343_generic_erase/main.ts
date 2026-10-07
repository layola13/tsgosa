class Stack<T> {
  items: T[] = [];
  push(x: T): void {
    this.items.push(x);
  }
  size(): number {
    return this.items.length;
  }
}
class Box<T> {
  constructor(public v: T) {}
}
function main(): i32 {
  const s = new Stack<number>();
  s.push(1);
  s.push(2);
  console.log(s.size());
  const b = new Box<number>(3);
  console.log(b.v);
  console.log(s.items.length);
  return 0;
}
