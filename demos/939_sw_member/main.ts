interface P { v: i32; }
function main(): i32 {
  const o: P = { v: 2 };
  switch (o.v) {
    case 1: console.log(1); break;
    case 2: console.log(2); break;
    default: console.log(0);
  }
  return 0;
}
