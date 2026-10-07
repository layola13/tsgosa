class Counter {
  n: i32 = 1;
  #hits: i32 = 0;
  inc(): this {
    this.n++;
    ++this.#hits;
    return this;
  }
  hits(): i32 { return this.#hits; }
}
class Vec {
  constructor(public x: i32, public y: i32) {}
  add(o: Vec): Vec { return new Vec(this.x + o.x, this.y + o.y); }
  scale(k: i32): Vec { const t = new Vec(this.x * k, this.y * k); return t; }
}
function main(): i32 {
  const c = new Counter();
  c.inc().inc();
  const d = c.inc();
  console.log(d.n);
  console.log(c.hits());
  const v = new Vec(1, 2).add(new Vec(3, 4));
  console.log(v.x + v.y);
  const w = v.add(v).scale(2);
  console.log(w.x);
  w.y--;
  console.log(w.y);
  return 0;
}
