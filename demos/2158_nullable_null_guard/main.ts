class Node {
  v: number;
  next: Node | null;
  constructor(v: number, next: Node | null) { this.v = v; this.next = next; }
}
function main(): i32 {
  const n2 = new Node(2, null);
  console.log(n2.next?.v ?? -1);
  return 0;
}
