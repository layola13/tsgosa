interface P {
  x: number;
}

function mk(): P {
  return { x: 4 };
}

export function main(): i32 {
  const o = { v: mk() };
  console.log(o.v.x);
  return 0;
}
