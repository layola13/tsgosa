class Base { constructor(public name: string) {} greet(): string { return `hi ${this.name}`; } }
class Kid extends Base { constructor() { super("kid"); } greet(): string { return super.greet() + "!"; } }
function main(): number { const k = new Kid(); console.log(k.greet()); return 0; }
