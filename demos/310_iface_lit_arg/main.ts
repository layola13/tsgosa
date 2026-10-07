interface A { x: number; }
interface B extends A { y: number; }
interface P { m: number; n: number; }
function add(o: B): number { return o.x + o.y; }
function sum(o: P): number { return o.m + o.n; }
function main(): number {
  const r1 = add({x: 1, y: 2});
  const p: B = {x: 3, y: 4};
  const r2 = add(p);
  const r3 = sum({m: 5, n: 6});
  console.log(r1);
  console.log(r2);
  console.log(r3);
  return r1 + r2 * 10 + r3 * 100;
}
console.log(main());
