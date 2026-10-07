interface Pt {
  x: number;
  s: string;
}

interface Wrap {
  ok: boolean;
  pt: Pt;
}

function main(): i32 {
  console.log(JSON.stringify(42));
  console.log(JSON.stringify("a"));
  console.log(JSON.stringify(true));
  console.log(JSON.stringify(null));
  const a: number[] = [1, 2];
  console.log(JSON.stringify(a));
  const b: string[] = ["x", "yy"];
  console.log(JSON.stringify(b));
  const p: Pt = {x: 1, s: "hi"};
  console.log(JSON.stringify(p));
  const w: Wrap = {ok: true, pt: {x: 9, s: "yo"}};
  console.log(JSON.stringify(w));
  return 0;
}
