interface P {
  a: number;
  b: string;
}

class Q {
  x: number = 1;
  y: number = 2;
}

export function main(): i32 {
  const p: P = { a: 1, b: "x" };
  const ks = Object.keys(p);
  console.log(ks.length);
  console.log(ks[0]);
  console.log(ks[1]);
  const q = new Q();
  const ks2 = Object.keys(q);
  console.log(ks2.length);
  console.log(ks2[0]);
  return 0;
}
