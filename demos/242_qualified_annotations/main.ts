namespace N {
  export class C {
    v: i32 = 0;
    constructor(n: i32) {
      this.v = n;
    }
  }
}
function take(c: N.C): number {
  return c.v;
}
function mk(): number {
  const tmp = new N.C(1);
  return tmp.v;
}
function main(): i32 {
  const c: N.C = new N.C(41);
  return take(c) + mk();
}
console.log(main());
