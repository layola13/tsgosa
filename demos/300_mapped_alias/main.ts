type O<T> = { [K in keyof T]: T[K] };
type P<T> = Partial<T>;
interface B { x: i32; }
interface C { x: i32; y: i32; }
function main(): i32 {
  const o: O<B> = { x: 1 };
  const p: P<C> = { x: 2, y: 3 };
  console.log(o.x + p.x + p.y);
  return o.x + p.x + p.y;
}
console.log(main());
