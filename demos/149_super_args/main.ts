class Pt {
  x: i32;
  constructor(x: i32) {
    this.x = x;
  }
}
class Q extends Pt {
  y: i32;
  constructor(x: i32, y: i32) {
    super(x);
    this.y = y;
  }
  sum(): i32 {
    return this.x + this.y;
  }
}
function main(): i32 {
  const q = new Q(3, 4);
  console.log(q.sum());
  return 0;
}