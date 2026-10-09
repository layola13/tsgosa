class B {
  v: i32 = 1;
  who(): i32 {
    return 10 + this.v;
  }
}
class D extends B {
  who(): i32 {
    return 100 + super.who();
  }
}
function main(): i32 {
  let s = 0;
  outer: for (let i = 0; i < 3; i++) {
    for (let j = 0; j < 3; j++) {
      if (j == 1) {
        continue outer;
      }
      s += 1;
    }
  }
  console.log(s);
  const d = new D();
  console.log(d.who());
  console.log(d.v);
  try {
    throw 7;
  } catch (e) {
    console.log(1);
  }
  return 0;
}
