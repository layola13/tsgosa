interface Item {
  v: i32;
}
interface Bag {
  items: Item[];
  n: i32;
}
function main(): i32 {
  const b: Bag = JSON.parse("{\"items\":[{\"v\":1},{\"v\":2}],\"n\":10}");
  const e0 = b.items[0];
  console.log(e0.v + b.items[1].v + b.n);
  return 0;
}
